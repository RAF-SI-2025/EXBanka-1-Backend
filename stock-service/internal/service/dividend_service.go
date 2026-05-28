package service

// dividend_service.go — E4 (Plan E, 2026-05-28)
//
// DividendService handles the two manual admin flows:
//   - Declare: create a DividendPayment row (idempotent on security+date).
//   - Payout:  fan out credits to every holder of the security.
//
// Tax treatment (per plan spec):
//   - client holdings:         15 % withheld; net_amount_rsd = 85 % of gross.
//   - bank holdings:           no tax; net = gross.
//   - investment_fund holding: no tax at payout time (deferred to redemption);
//     gross goes to the fund's RSD account; a FundDividendPayment snapshot is
//     written so composeFundPosition can compute per-investor dividends_received_rsd.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

var taxRate = decimal.NewFromFloat(0.15)

// DividendService implements the dividend infrastructure.
type DividendService struct {
	db               *gorm.DB
	paymentRepo      *repository.DividendPaymentRepository
	payoutRepo       *repository.DividendPayoutRepository
	fundPayoutRepo   *repository.FundDividendPaymentRepository
	holdingRepo      *repository.HoldingRepository
	fundHoldingRepo  *repository.FundHoldingRepository
	fundRepo         *repository.FundRepository
	fundPositionRepo *repository.ClientFundPositionRepository
	accounts         FundAccountClient
}

// NewDividendService constructs the service with required dependencies.
func NewDividendService(
	db *gorm.DB,
	paymentRepo *repository.DividendPaymentRepository,
	payoutRepo *repository.DividendPayoutRepository,
	fundPayoutRepo *repository.FundDividendPaymentRepository,
	holdingRepo *repository.HoldingRepository,
	fundHoldingRepo *repository.FundHoldingRepository,
	fundRepo *repository.FundRepository,
	fundPositionRepo *repository.ClientFundPositionRepository,
	accounts FundAccountClient,
) *DividendService {
	return &DividendService{
		db:              db,
		paymentRepo:     paymentRepo,
		payoutRepo:      payoutRepo,
		fundPayoutRepo:  fundPayoutRepo,
		holdingRepo:     holdingRepo,
		fundHoldingRepo: fundHoldingRepo,
		fundRepo:        fundRepo,
		fundPositionRepo: fundPositionRepo,
		accounts:        accounts,
	}
}

// DividendPayoutSummary is the response returned by Payout.
type DividendPayoutSummary struct {
	PayoutsCreated  int
	FundPayouts     int
	TotalAmountRSD  decimal.Decimal
}

// Declare creates a DividendPayment row. Idempotent on (security_id, payment_date):
// a second call with the same key returns the existing row.
func (s *DividendService) Declare(
	ctx context.Context,
	securityID uint64,
	ticker string,
	amountPerShareRSD decimal.Decimal,
	paymentDate time.Time,
	declaredByEmployeeID int64,
) (*model.DividendPayment, error) {
	if securityID == 0 {
		return nil, fmt.Errorf("security_id is required")
	}
	if ticker == "" {
		return nil, fmt.Errorf("ticker is required")
	}
	if !amountPerShareRSD.IsPositive() {
		return nil, fmt.Errorf("amount_per_share_rsd must be positive")
	}

	dp := &model.DividendPayment{
		SecurityID:           securityID,
		Ticker:               strings.ToUpper(ticker),
		AmountPerShareRSD:    amountPerShareRSD,
		PaymentDate:          paymentDate.UTC().Truncate(24 * time.Hour),
		Status:               "declared",
		DeclaredByEmployeeID: declaredByEmployeeID,
	}
	if err := s.paymentRepo.Create(dp); err != nil {
		return nil, fmt.Errorf("declare dividend: %w", err)
	}

	// If RowsAffected was 0 (idempotent DoNothing path), re-fetch the existing row.
	if dp.ID == 0 {
		existing, err := s.paymentRepo.GetBySecurityAndDate(securityID, dp.PaymentDate)
		if err != nil {
			return nil, fmt.Errorf("reload existing dividend payment: %w", err)
		}
		return existing, nil
	}
	return dp, nil
}

// Payout walks every holding (client/bank/investment_fund) of the security,
// computes and credits dividends, and records DividendPayout rows.
// Idempotent per (payment_id, holding_id) via the unique idempotency_key.
//
// Account credits go OUTSIDE the local DB transaction (cross-service call);
// the idempotency key prevents double-credit on retry.
func (s *DividendService) Payout(ctx context.Context, dividendPaymentID uint64) (DividendPayoutSummary, error) {
	payment, err := s.paymentRepo.GetByID(dividendPaymentID)
	if err != nil {
		return DividendPayoutSummary{}, fmt.Errorf("get dividend payment %d: %w", dividendPaymentID, err)
	}
	if payment.Status == "paid_out" {
		return DividendPayoutSummary{}, fmt.Errorf("dividend payment %d is already paid out", dividendPaymentID)
	}
	if payment.Status == "cancelled" {
		return DividendPayoutSummary{}, fmt.Errorf("dividend payment %d is cancelled", dividendPaymentID)
	}

	var summary DividendPayoutSummary

	// ── Direct holdings (client + bank) ──────────────────────────────────────
	holdings, err := s.holdingRepo.ListBySecurityID(payment.SecurityID)
	if err != nil {
		return DividendPayoutSummary{}, fmt.Errorf("list holdings for security %d: %w", payment.SecurityID, err)
	}

	for _, h := range holdings {
		ikey := fmt.Sprintf("dividend-%d-%d", dividendPaymentID, h.ID)

		// Check idempotency (already paid for this holding).
		if _, err := s.payoutRepo.GetByIdempotencyKey(ikey); err == nil {
			continue // already exists
		}

		gross := payment.AmountPerShareRSD.Mul(decimal.NewFromInt(h.Quantity))
		var tax decimal.Decimal
		if string(h.OwnerType) == "client" {
			tax = gross.Mul(taxRate).Round(4)
		}
		net := gross.Sub(tax)

		// Get the holder's RSD account to credit.
		accountID, accountNumber, err := s.resolveHolderRSDAccount(ctx, h.AccountID, h.OwnerType, h.OwnerID)
		if err != nil {
			log.Printf("WARN: dividend payout: resolve account for holding %d: %v — skipping", h.ID, err)
			continue
		}

		// Credit the account (outside DB tx per CLAUDE.md cross-service rule).
		creditMemo := fmt.Sprintf("Dividend %s %.4f/share × %d shares", payment.Ticker, payment.AmountPerShareRSD.InexactFloat64(), h.Quantity)
		creditKey := fmt.Sprintf("dividend-credit-%d-%d", dividendPaymentID, h.ID)
		_, creditErr := s.accounts.CreditAccount(ctx, accountNumber, net, creditMemo, creditKey)
		if creditErr != nil {
			log.Printf("WARN: dividend payout: credit account %s for holding %d: %v — skipping", accountNumber, h.ID, creditErr)
			continue
		}

		// Record the payout row.
		payout := &model.DividendPayout{
			DividendPaymentID: dividendPaymentID,
			HoldingOwnerType:  string(h.OwnerType),
			HoldingOwnerID:    h.OwnerID,
			HoldingID:         h.ID,
			Shares:            h.Quantity,
			GrossAmountRSD:    gross,
			TaxAmountRSD:      tax,
			NetAmountRSD:      net,
			CreditedAccountID: accountID,
			IdempotencyKey:    ikey,
		}
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			return s.payoutRepo.Create(tx, payout)
		}); err != nil {
			log.Printf("WARN: dividend payout: save payout row for holding %d: %v", h.ID, err)
			continue
		}

		summary.PayoutsCreated++
		summary.TotalAmountRSD = summary.TotalAmountRSD.Add(net)
	}

	// ── Fund holdings ─────────────────────────────────────────────────────────
	fundHoldings, err := s.fundHoldingRepo.ListBySecurityID(payment.SecurityID)
	if err != nil {
		return DividendPayoutSummary{}, fmt.Errorf("list fund holdings for security %d: %w", payment.SecurityID, err)
	}

	for _, fh := range fundHoldings {
		ikey := fmt.Sprintf("dividend-fund-%d-%d", dividendPaymentID, fh.ID)

		// Check idempotency.
		if _, err := s.payoutRepo.GetByIdempotencyKey(ikey); err == nil {
			continue
		}

		gross := payment.AmountPerShareRSD.Mul(decimal.NewFromInt(fh.Quantity))
		// No tax at fund level — deferred to investor redemption.

		// Fetch the fund to get its RSD account.
		fund, err := s.fundRepo.GetByID(fh.FundID)
		if err != nil {
			log.Printf("WARN: dividend payout: get fund %d: %v — skipping", fh.FundID, err)
			continue
		}

		// Get the fund's RSD account number.
		acctResp, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
		if err != nil {
			log.Printf("WARN: dividend payout: get fund RSD account %d: %v — skipping", fund.RSDAccountID, err)
			continue
		}

		// Credit the fund's RSD account.
		creditMemo := fmt.Sprintf("Dividend %s %.4f/share × %d shares (fund %d)", payment.Ticker, payment.AmountPerShareRSD.InexactFloat64(), fh.Quantity, fh.FundID)
		creditKey := fmt.Sprintf("dividend-credit-fund-%d-%d", dividendPaymentID, fh.ID)
		_, creditErr := s.accounts.CreditAccount(ctx, acctResp.AccountNumber, gross, creditMemo, creditKey)
		if creditErr != nil {
			log.Printf("WARN: dividend payout: credit fund account %s: %v — skipping", acctResp.AccountNumber, creditErr)
			continue
		}

		// Compute per-investor snapshot (pct_of_fund at payment time).
		snapshot, err := s.buildInvestorSnapshot(fh.FundID, gross)
		if err != nil {
			log.Printf("WARN: dividend payout: build investor snapshot for fund %d: %v — continuing without snapshot", fh.FundID, err)
			snapshot = "[]"
		}

		// Record the fund payout row + fund_dividend_payment atomically.
		ownerID := uint64(fh.FundID)
		payout := &model.DividendPayout{
			DividendPaymentID: dividendPaymentID,
			HoldingOwnerType:  "investment_fund",
			HoldingOwnerID:    &ownerID,
			HoldingID:         fh.ID,
			Shares:            fh.Quantity,
			GrossAmountRSD:    gross,
			TaxAmountRSD:      decimal.Zero,
			NetAmountRSD:      gross,
			CreditedAccountID: fund.RSDAccountID,
			IdempotencyKey:    ikey,
		}
		fdp := &model.FundDividendPayment{
			DividendPaymentID:   dividendPaymentID,
			FundID:              fh.FundID,
			AmountRSD:           gross,
			PerInvestorSnapshot: snapshot,
		}
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := s.payoutRepo.Create(tx, payout); err != nil {
				return err
			}
			return s.fundPayoutRepo.Create(tx, fdp)
		}); err != nil {
			log.Printf("WARN: dividend payout: save fund payout rows for holding %d / fund %d: %v", fh.ID, fh.FundID, err)
			continue
		}

		summary.FundPayouts++
		summary.TotalAmountRSD = summary.TotalAmountRSD.Add(gross)
	}

	// Mark payment as paid_out.
	now := time.Now().UTC()
	if err := s.paymentRepo.MarkPaidOut(dividendPaymentID, now); err != nil {
		log.Printf("WARN: dividend payout: mark payment %d paid_out: %v", dividendPaymentID, err)
	}

	return summary, nil
}

// GetPayment returns a DividendPayment by ID.
func (s *DividendService) GetPayment(id uint64) (*model.DividendPayment, error) {
	return s.paymentRepo.GetByID(id)
}

// ListMyDividends returns paginated dividend_payouts for the caller.
func (s *DividendService) ListMyDividends(ownerType string, ownerID *uint64, page, pageSize int) ([]model.DividendPayout, int64, error) {
	return s.payoutRepo.ListByOwner(ownerType, ownerID, page, pageSize)
}

// ListFundDividends returns paginated fund_dividend_payments for a fund.
func (s *DividendService) ListFundDividends(fundID uint64, page, pageSize int) ([]model.FundDividendPayment, int64, error) {
	return s.fundPayoutRepo.ListByFundID(fundID, page, pageSize)
}

// SumDividendsByFund returns the total dividends credited to a fund (all time).
// Used by FundService.Statistics to populate total_dividends_paid_rsd.
func (s *DividendService) SumDividendsByFund(fundID uint64) (decimal.Decimal, error) {
	return s.fundPayoutRepo.SumByFundID(fundID)
}

// SumDividendsReceivedByOwner returns the total net dividends received by an owner
// across all their direct holdings.
func (s *DividendService) SumDividendsReceivedByOwner(ownerType string, ownerID *uint64) (decimal.Decimal, error) {
	return s.payoutRepo.SumNetByOwner(ownerType, ownerID)
}

// DividendsReceivedByFundInvestor computes the pro-rata share of dividends
// an investor received from a specific fund, using the per_investor_snapshot
// stored in each FundDividendPayment row.
func (s *DividendService) DividendsReceivedByFundInvestor(fundID uint64, investorOwnerType string, investorOwnerID *uint64) (decimal.Decimal, error) {
	rows, _, err := s.fundPayoutRepo.ListByFundID(fundID, 1, 10000)
	if err != nil {
		return decimal.Zero, err
	}

	total := decimal.Zero
	for _, fdp := range rows {
		var snapshots []model.InvestorShareSnapshot
		if err := json.Unmarshal([]byte(fdp.PerInvestorSnapshot), &snapshots); err != nil {
			continue
		}
		for _, snap := range snapshots {
			if snap.InvestorOwnerType != investorOwnerType {
				continue
			}
			if !ownerIDsMatch(snap.InvestorOwnerID, investorOwnerID) {
				continue
			}
			if d, err := decimal.NewFromString(snap.GrossShareRSD); err == nil {
				total = total.Add(d)
			}
		}
	}
	return total, nil
}

// ownerIDsMatch compares two nullable *uint64 values (nil = bank sentinel).
func ownerIDsMatch(a, b *uint64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// resolveHolderRSDAccount returns the account_id and account_number of the
// holder's RSD account. For clients: uses the holding's AccountID (the last
// account the holder used to buy the security). For bank: uses GetBankRSDAccount
// (not available here — falls back to accountID from holding row).
//
// In both cases the account is fetched via GetAccount so we get its number.
func (s *DividendService) resolveHolderRSDAccount(ctx context.Context, holdingAccountID uint64, ownerType model.OwnerType, ownerID *uint64) (uint64, string, error) {
	if holdingAccountID == 0 {
		return 0, "", fmt.Errorf("holding has no account_id")
	}
	resp, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: holdingAccountID})
	if err != nil {
		return 0, "", fmt.Errorf("get account %d: %w", holdingAccountID, err)
	}
	return resp.Id, resp.AccountNumber, nil
}

// buildInvestorSnapshot computes the per-investor contribution percentages
// at the current moment (a proxy for "at payment time" for the MVP) and
// returns a JSON-encoded []InvestorShareSnapshot.
func (s *DividendService) buildInvestorSnapshot(fundID uint64, totalFundDividend decimal.Decimal) (string, error) {
	positions, err := s.fundPositionRepo.ListByFund(fundID)
	if err != nil {
		return "[]", err
	}

	// Compute total contributed across all investors for the fund.
	var totalContributed decimal.Decimal
	for _, p := range positions {
		totalContributed = totalContributed.Add(p.TotalContributedRSD)
	}

	var snapshots []model.InvestorShareSnapshot
	for _, p := range positions {
		pct := decimal.Zero
		grossShare := decimal.Zero
		if !totalContributed.IsZero() {
			pct = p.TotalContributedRSD.Div(totalContributed).Mul(decimal.NewFromInt(100))
			grossShare = totalFundDividend.Mul(pct).Div(decimal.NewFromInt(100))
		}
		snap := model.InvestorShareSnapshot{
			InvestorOwnerType: string(p.OwnerType),
			InvestorOwnerID:   p.OwnerID,
			PctAtPayment:      pct.StringFixed(4),
			GrossShareRSD:     grossShare.StringFixed(4),
		}
		snapshots = append(snapshots, snap)
	}

	b, err := json.Marshal(snapshots)
	if err != nil {
		return "[]", err
	}
	return string(b), nil
}
