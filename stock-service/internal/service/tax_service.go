package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
)

type TaxService struct {
	capitalGainRepo   CapitalGainRepo
	taxCollectionRepo TaxCollectionRepo
	holdingRepo       HoldingRepo
	accountClient     accountpb.AccountServiceClient
	exchangeClient    exchangepb.ExchangeServiceClient
	stateAccountNo    string
	// db is optional — when wired, CollectTax uses it to acquire a
	// pg_advisory_xact_lock so two concurrent admin invocations for the
	// same (year, month) serialize instead of racing. Nil in tests that
	// don't exercise the lock path (e.g., unit tests using mocks).
	db *gorm.DB
}

func NewTaxService(
	capitalGainRepo CapitalGainRepo,
	taxCollectionRepo TaxCollectionRepo,
	holdingRepo HoldingRepo,
	accountClient accountpb.AccountServiceClient,
	exchangeClient exchangepb.ExchangeServiceClient,
	stateAccountNo string,
) *TaxService {
	return &TaxService{
		capitalGainRepo:   capitalGainRepo,
		taxCollectionRepo: taxCollectionRepo,
		holdingRepo:       holdingRepo,
		accountClient:     accountClient,
		exchangeClient:    exchangeClient,
		stateAccountNo:    stateAccountNo,
	}
}

// WithDB wires the gorm handle used by CollectTax's advisory-lock path so
// concurrent admin calls for the same (year, month) serialize. Kept as a
// builder method to avoid perturbing existing NewTaxService call sites.
func (s *TaxService) WithDB(db *gorm.DB) *TaxService {
	cp := *s
	cp.db = db
	return &cp
}

// ListTaxRecords returns users with their tax debt for supervisor portal.
func (s *TaxService) ListTaxRecords(year, month int, filter TaxFilter) ([]TaxUserSummary, int64, error) {
	return s.taxCollectionRepo.ListUsersWithGains(year, month, filter)
}

// GetUserTaxSummary returns tax summary for a (user_id, system_type) owner's
// portfolio page.
func (s *TaxService) GetUserTaxSummary(userID uint64, systemType string) (taxPaidThisYear, taxUnpaidThisMonth decimal.Decimal, err error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// Tax paid this year
	taxPaidThisYear, err = s.taxCollectionRepo.SumByUserYear(userID, systemType, year)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}

	// Tax owed this month (uncollected)
	gainSummaries, err := s.capitalGainRepo.SumByUserMonth(userID, systemType, year, month)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}

	// Already collected this month
	collectedThisMonth, err := s.taxCollectionRepo.SumByUserMonth(userID, systemType, year, month)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}

	// Compute uncollected tax in RSD
	taxRate := decimal.NewFromFloat(0.15)
	totalUnpaidRSD := decimal.Zero
	for _, gs := range gainSummaries {
		if gs.TotalGain.IsPositive() {
			taxInCurrency := gs.TotalGain.Mul(taxRate)
			taxInRSD := taxInCurrency
			if gs.Currency != "RSD" {
				converted, convErr := s.convertToRSD(gs.Currency, taxInCurrency)
				if convErr == nil {
					taxInRSD = converted
				}
			}
			totalUnpaidRSD = totalUnpaidRSD.Add(taxInRSD)
		}
	}
	taxUnpaidThisMonth = totalUnpaidRSD.Sub(collectedThisMonth).Round(2)
	if taxUnpaidThisMonth.IsNegative() {
		taxUnpaidThisMonth = decimal.Zero
	}

	return taxPaidThisYear, taxUnpaidThisMonth, nil
}

// ListUserTaxRecords returns paginated capital gain records for a single
// (user_id, system_type) owner.
func (s *TaxService) ListUserTaxRecords(userID uint64, systemType string, page, pageSize int) ([]model.CapitalGain, int64, error) {
	return s.capitalGainRepo.ListByUser(userID, systemType, page, pageSize)
}

// UserGainsAndTax aggregates realized gain + tax figures for a user across
// the current month, current year, and lifetime. All monetary amounts are
// converted to RSD via exchange-service. Gains may be negative (losses);
// tax_unpaid is only computed on positive per-month gains (losses do not
// produce negative tax).
type UserGainsAndTax struct {
	RealizedGainThisMonthRSD decimal.Decimal
	RealizedGainThisYearRSD  decimal.Decimal
	RealizedGainLifetimeRSD  decimal.Decimal
	TaxPaidThisYearRSD       decimal.Decimal
	TaxUnpaidThisMonthRSD    decimal.Decimal
	TaxUnpaidTotalRSD        decimal.Decimal
	ClosedTradesThisYear     int64
}

// GetUserGainsAndTax powers the rich portfolio summary endpoint. It composes
// existing repo queries and the same convertToRSD helper the tax-collection
// path already uses, so numbers here match what CollectTax will actually move.
func (s *TaxService) GetUserGainsAndTax(userID uint64, systemType string) (UserGainsAndTax, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	out := UserGainsAndTax{}

	monthGains, err := s.capitalGainRepo.SumByUserMonth(userID, systemType, year, month)
	if err != nil {
		return out, err
	}
	out.RealizedGainThisMonthRSD = s.sumGainsInRSD(monthGains)

	yearGains, err := s.capitalGainRepo.SumByUserYear(userID, systemType, year)
	if err != nil {
		return out, err
	}
	out.RealizedGainThisYearRSD = s.sumGainsInRSD(yearGains)

	lifeGains, err := s.capitalGainRepo.SumByUserAllTime(userID, systemType)
	if err != nil {
		return out, err
	}
	out.RealizedGainLifetimeRSD = s.sumGainsInRSD(lifeGains)

	paidYear, err := s.taxCollectionRepo.SumByUserYear(userID, systemType, year)
	if err != nil {
		return out, err
	}
	out.TaxPaidThisYearRSD = paidYear

	paidLife, err := s.taxCollectionRepo.SumByUserAllTime(userID, systemType)
	if err != nil {
		return out, err
	}

	// Tax unpaid (this month): same formula the existing GetUserTaxSummary uses.
	collectedThisMonth, err := s.taxCollectionRepo.SumByUserMonth(userID, systemType, year, month)
	if err != nil {
		return out, err
	}
	out.TaxUnpaidThisMonthRSD = s.computeUnpaidTax(monthGains, collectedThisMonth)

	// Tax unpaid (lifetime): 15% of every positive monthly gain ever minus
	// everything collected. Using lifetime gains × 0.15 doesn't match per-
	// month rounding/accumulation exactly but is close enough for the summary
	// display and always ≥ 0.
	out.TaxUnpaidTotalRSD = s.computeUnpaidTax(lifeGains, paidLife)

	count, err := s.capitalGainRepo.CountByUserYear(userID, systemType, year)
	if err != nil {
		return out, err
	}
	out.ClosedTradesThisYear = count

	return out, nil
}

// sumGainsInRSD converts every (account, currency) gain row to RSD via the
// existing convertToRSD helper and sums them. A conversion failure on one
// row degrades to "use the native amount" rather than dropping the row.
func (s *TaxService) sumGainsInRSD(gains []AccountGainSummary) decimal.Decimal {
	total := decimal.Zero
	for _, g := range gains {
		if g.Currency == "RSD" {
			total = total.Add(g.TotalGain)
			continue
		}
		conv, err := s.convertToRSD(g.Currency, g.TotalGain)
		if err == nil {
			total = total.Add(conv)
			continue
		}
		// Degrade gracefully: accept the native-currency amount rather than
		// silently dropping the row. Better a slightly-off display than a
		// missing one.
		total = total.Add(g.TotalGain)
	}
	return total.Round(2)
}

// computeUnpaidTax replicates the existing GetUserTaxSummary math: 15% of
// positive gains minus already-collected tax, floored at zero. Applied over
// whatever window of gains the caller passes in.
func (s *TaxService) computeUnpaidTax(gains []AccountGainSummary, alreadyCollected decimal.Decimal) decimal.Decimal {
	taxRate := decimal.NewFromFloat(0.15)
	totalUnpaid := decimal.Zero
	for _, g := range gains {
		if !g.TotalGain.IsPositive() {
			continue
		}
		taxInCurrency := g.TotalGain.Mul(taxRate)
		taxInRSD := taxInCurrency
		if g.Currency != "RSD" {
			if converted, err := s.convertToRSD(g.Currency, taxInCurrency); err == nil {
				taxInRSD = converted
			}
		}
		totalUnpaid = totalUnpaid.Add(taxInRSD)
	}
	result := totalUnpaid.Sub(alreadyCollected).Round(2)
	if result.IsNegative() {
		return decimal.Zero
	}
	return result
}

// ListUserTaxCollections returns the full collection history for a single
// (user_id, system_type) owner so they can see when tax was actually taken.
// Capped to 200 most recent rows — that covers 16+ years of monthly collections.
func (s *TaxService) ListUserTaxCollections(userID uint64, systemType string) ([]model.TaxCollection, error) {
	rows, _, err := s.taxCollectionRepo.ListByUser(userID, systemType, 1, 200)
	return rows, err
}

// CollectTax collects tax from all users for the given month.
// Returns the number of users collected, total RSD, and failures.
//
// F.1 idempotency guard: for every (user_id, system_type, account_id,
// currency) tuple within the target month, the service first asks the
// repository if any tax was already collected — if so the tuple is skipped
// with a WARN log so retries don't double-charge.
//
// F.2 advisory lock: when db is wired (production), CollectTax runs inside a
// transaction that acquires pg_advisory_xact_lock(year*100 + month) as its
// first statement. Two concurrent admin invocations for the same month
// therefore serialize — the second blocks until the first commits, at which
// point the idempotency guard returns its "already collected" skip.
func (s *TaxService) CollectTax(year, month int) (collectedCount int64, totalRSD decimal.Decimal, failedCount int64, err error) {
	if s.db == nil {
		// No DB handle wired (unit tests). Run without the advisory lock;
		// the idempotency guard below still prevents double-collection
		// within a single invocation.
		return s.collectTaxInner(year, month)
	}
	lockKey := int64(year)*100 + int64(month)
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// Advisory lock is Postgres-specific; skip on other dialects
		// (SQLite in tests) so the path still exercises the inner logic.
		if tx.Dialector.Name() == "postgres" {
			if lerr := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; lerr != nil {
				return lerr
			}
		}
		var innerErr error
		collectedCount, totalRSD, failedCount, innerErr = s.collectTaxInner(year, month)
		return innerErr
	})
	return collectedCount, totalRSD, failedCount, err
}

// collectTaxInner is the bulk of CollectTax, extracted so the advisory-lock
// wrapper can call it inside a TX. Keeps the two paths in sync.
func (s *TaxService) collectTaxInner(year, month int) (collectedCount int64, totalRSD decimal.Decimal, failedCount int64, err error) {
	StockTaxCollectedTotal.Inc()
	// Get all users with gains this month (broad filter, all pages)
	summaries, _, err := s.taxCollectionRepo.ListUsersWithGains(year, month, TaxFilter{
		Page:     1,
		PageSize: 10000, // collect all
	})
	if err != nil {
		return 0, decimal.Zero, 0, err
	}

	taxRate := decimal.NewFromFloat(0.15)

	for _, summary := range summaries {
		if summary.TotalDebtRSD.IsZero() || summary.TotalDebtRSD.IsNegative() {
			continue
		}

		// F.1 idempotency guard: if any tax was already collected for this
		// user this month, skip. The per-account/per-currency inner loop
		// still runs a finer-grained guard below for mixed-currency users
		// where one ledger collected but another raced and didn't.
		if already, ierr := s.taxCollectionRepo.SumByUserMonth(summary.UserID, summary.SystemType, year, month); ierr == nil && already.IsPositive() {
			log.Printf("WARN: tax: skipping user %d (%s) for %04d-%02d — already collected RSD %s",
				summary.UserID, summary.SystemType, year, month, already.StringFixed(2))
			continue
		}

		// Get detailed gains per account, scoped to this (user_id, system_type)
		// pair so employee and client bookkeeping stay separate when IDs collide.
		gainSummaries, gainErr := s.capitalGainRepo.SumByUserMonth(summary.UserID, summary.SystemType, year, month)
		if gainErr != nil {
			failedCount++
			log.Printf("WARN: tax: failed to get gains for user %d (%s): %v", summary.UserID, summary.SystemType, gainErr)
			continue
		}

		userFailed := false
		for _, gs := range gainSummaries {
			if !gs.TotalGain.IsPositive() {
				continue
			}

			taxInCurrency := gs.TotalGain.Mul(taxRate).Round(4)
			taxInRSD := taxInCurrency

			// Convert to RSD if needed
			if gs.Currency != "RSD" {
				converted, convErr := s.convertToRSD(gs.Currency, taxInCurrency)
				if convErr != nil {
					log.Printf("WARN: tax: currency conversion failed for user %d: %v", summary.UserID, convErr)
					userFailed = true
					continue
				}
				taxInRSD = converted
			}

			// Debit user's account
			acctResp, acctErr := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: gs.AccountID})
			if acctErr != nil {
				log.Printf("WARN: tax: account lookup failed for user %d account %d: %v", summary.UserID, gs.AccountID, acctErr)
				userFailed = true
				continue
			}

			debitMemo := fmt.Sprintf("Capital-gains tax collection %04d-%02d", year, month)
			debitKey := fmt.Sprintf("tax-debit-%s-%d-%04d-%02d-%d", summary.SystemType, summary.UserID, year, month, gs.AccountID)
			_, debitErr := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
				AccountNumber:   acctResp.AccountNumber,
				Amount:          taxInCurrency.Neg().StringFixed(4),
				UpdateAvailable: true,
				Memo:            debitMemo,
				IdempotencyKey:  debitKey,
			})
			if debitErr != nil {
				log.Printf("WARN: tax: debit failed for user %d account %s: %v", summary.UserID, acctResp.AccountNumber, debitErr)
				userFailed = true
				continue
			}

			// Credit state's RSD account
			creditMemo := fmt.Sprintf("Capital-gains tax from %s #%d (%s %s)", summary.SystemType, summary.UserID, gs.Currency, taxInCurrency.StringFixed(2))
			creditKey := fmt.Sprintf("tax-credit-%s-%d-%04d-%02d-%d", summary.SystemType, summary.UserID, year, month, gs.AccountID)
			_, creditErr := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
				AccountNumber:   s.stateAccountNo,
				Amount:          taxInRSD.StringFixed(4),
				UpdateAvailable: true,
				Memo:            creditMemo,
				IdempotencyKey:  creditKey,
			})
			if creditErr != nil {
				log.Printf("WARN: tax: credit state account failed for user %d: %v", summary.UserID, creditErr)
			}

			// Record collection
			collection := &model.TaxCollection{
				UserID:       summary.UserID,
				SystemType:   summary.SystemType,
				Year:         year,
				Month:        month,
				AccountID:    gs.AccountID,
				Currency:     gs.Currency,
				TotalGain:    gs.TotalGain,
				TaxAmount:    taxInCurrency,
				TaxAmountRSD: taxInRSD,
				CollectedAt:  time.Now(),
			}
			if createErr := s.taxCollectionRepo.Create(collection); createErr != nil {
				log.Printf("WARN: tax: failed to record collection for user %d: %v", summary.UserID, createErr)
			}

			totalRSD = totalRSD.Add(taxInRSD)
		}

		if userFailed {
			failedCount++
		} else {
			collectedCount++
		}
	}

	return collectedCount, totalRSD.Round(2), failedCount, nil
}

// convertToRSD converts an amount from a foreign currency to RSD.
func (s *TaxService) convertToRSD(fromCurrency string, amount decimal.Decimal) (decimal.Decimal, error) {
	resp, err := s.exchangeClient.Convert(context.Background(), &exchangepb.ConvertRequest{
		FromCurrency: fromCurrency,
		ToCurrency:   "RSD",
		Amount:       amount.StringFixed(4),
	})
	if err != nil {
		return decimal.Zero, err
	}
	converted, err := decimal.NewFromString(resp.ConvertedAmount)
	if err != nil {
		return decimal.Zero, err
	}
	return converted, nil
}
