package service

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// BankCommissionRecipient resolves the bank's commission account number so
// ForexFillService can credit the house without pulling in all of
// PortfolioService's bank-account lookup logic. main.go passes a small
// adapter that returns the pre-seeded state-account number.
type BankCommissionRecipient interface {
	BankCommissionAccountNumber(ctx context.Context) (string, error)
}

// ForexFillService handles the settlement side of a forex fill.
//
// Forex orders follow the same placement pipeline as stocks (reservations +
// saga) but their fill is a pure intra-user transfer: the buyer pays in the
// pair's quote currency and receives base currency on their own base
// account. No holding row is touched, no exchange-service.Convert call is
// made — the forex listing's own price is the conversion rate.
//
// Saga shape for a forex buy:
//
//	record_transaction          (no-op; caller persisted the txn)
//	settle_reservation_quote    (PartialSettleReservation on quote account)
//	credit_base                 (CreditAccount on base account)
//	credit_commission           (best-effort, on the quote/state account)
//
// Compensation matrix:
//
//	record_transaction   → none
//	settle_reservation   → none (reservation stays; fill loop retries)
//	credit_base          → reverse-credit the quote account (refund the user)
//	credit_commission    → log only, trade remains valid
type ForexFillService struct {
	sagaRepo      FillSagaLogRepo
	accountClient FillAccountClient
	txRepo        OrderTransactionRepo
	settings      OrderSettings
	bankRecipient BankCommissionRecipient
}

// NewForexFillService constructs the forex-specific fill service.
// `settings` may be nil; the commission rate then falls back to
// defaultCommissionRate (0.25%). `bankRecipient` may be nil, in which case
// the commission step is skipped entirely (no state-account credit).
func NewForexFillService(
	sagaRepo FillSagaLogRepo,
	accountClient FillAccountClient,
	txRepo OrderTransactionRepo,
	settings OrderSettings,
	bankRecipient BankCommissionRecipient,
) *ForexFillService {
	return &ForexFillService{
		sagaRepo:      sagaRepo,
		accountClient: accountClient,
		txRepo:        txRepo,
		settings:      settings,
		bankRecipient: bankRecipient,
	}
}

// ProcessForexBuy settles a forex buy fill by debiting the quote-currency
// account (where funds were reserved at placement) and crediting the
// base-currency account. Commission is credited to the bank's state
// account on a best-effort basis.
//
// For a forex pair BASE/QUOTE with contract_size C and execution price p on
// a fill of quantity q:
//   - quoteAmount = txn.TotalPrice  (= p × q × C, in QUOTE currency)
//   - baseAmount  = q × C            (in BASE currency)
//
// The order.BaseAccountID field (populated at placement time) identifies
// the base-currency account to credit; order.AccountID / order.ReservationAccountID
// identifies the quote-currency account where funds were reserved.
func (s *ForexFillService) ProcessForexBuy(ctx context.Context, order *model.Order, txn *model.OrderTransaction) error {
	if order.SecurityType != "forex" {
		return fmt.Errorf("forex fill: unexpected security_type %q", order.SecurityType)
	}
	if order.BaseAccountID == nil {
		return fmt.Errorf("forex fill order %d: missing base_account_id", order.ID)
	}

	sagaID := order.SagaID
	if sagaID == "" {
		sagaID = uuid.New().String()
	}
	txnID := txn.ID
	exec := NewSagaExecutor(s.sagaRepo, sagaID, order.ID, &txnID)

	// --- Step 1: record_transaction (no-op; caller already persisted the txn) ---
	if err := exec.RunStep(ctx, "record_transaction", txn.TotalPrice, order.ReservationCurrency, nil, func() error {
		return nil
	}); err != nil {
		return err
	}

	// Compute the base-currency amount. For forex the "quantity" on the txn is
	// the number of lots; the settlement converts the quote total back to
	// baseAmount = quantity × contract_size. No exchange-service call — the
	// forex listing's own price is the conversion rate (already baked into
	// txn.TotalPrice at execution time).
	contractSize := order.ContractSize
	if contractSize <= 0 {
		contractSize = 1
	}
	quoteAmount := txn.TotalPrice
	baseAmount := decimal.NewFromInt(txn.Quantity).Mul(decimal.NewFromInt(contractSize))

	// Persist audit fields on the transaction. No FX rate is recorded because
	// no exchange-service conversion happened; the effective rate is the
	// forex listing's own price (txn.PricePerUnit), which is already stored.
	native := quoteAmount
	txn.NativeAmount = &native
	txn.NativeCurrency = order.ReservationCurrency
	converted := quoteAmount
	txn.ConvertedAmount = &converted
	txn.AccountCurrency = order.ReservationCurrency
	if err := s.txRepo.Update(txn); err != nil {
		return fmt.Errorf("persist txn audit fields: %w", err)
	}

	// --- Step 2: settle_reservation_quote ---
	quoteMemo := fmt.Sprintf("Forex buy order #%d fill #%d — debit quote", order.ID, txn.ID)
	if err := exec.RunStep(ctx, "settle_reservation_quote", quoteAmount, order.ReservationCurrency,
		map[string]any{"account_id": order.AccountID, "txn_id": txn.ID}, func() error {
			_, serr := s.accountClient.PartialSettleReservation(ctx, order.ID, txn.ID, quoteAmount, quoteMemo)
			return serr
		}); err != nil {
		return err
	}
	// Look up the forward step ID for compensation linking below.
	var quoteStepID uint64
	if step, lerr := s.sagaRepo.GetByStepName(order.ID, "settle_reservation_quote"); lerr == nil && step != nil {
		quoteStepID = step.ID
	}

	// --- Step 3: credit_base ---
	// Look up the base account's number (the user's base-currency account).
	baseAcct, err := s.accountClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: *order.BaseAccountID})
	if err != nil {
		// The quote account has already been debited — compensate by
		// reverse-crediting the same amount back to the quote account so
		// the user is made whole.
		s.compensateQuote(ctx, exec, order, txn, quoteStepID, quoteAmount)
		return fmt.Errorf("base account lookup: %w", err)
	}
	baseMemo := fmt.Sprintf("Forex buy order #%d fill #%d — credit base", order.ID, txn.ID)
	if err := exec.RunStep(ctx, "credit_base", baseAmount, baseAcct.CurrencyCode,
		map[string]any{"base_account_id": *order.BaseAccountID}, func() error {
			_, cerr := s.accountClient.CreditAccount(ctx, baseAcct.AccountNumber, baseAmount, baseMemo)
			return cerr
		}); err != nil {
		// Compensate: reverse-credit the quote account for the settled amount.
		s.compensateQuote(ctx, exec, order, txn, quoteStepID, quoteAmount)
		return err
	}

	// --- Step 4: credit_commission (best-effort; do NOT fail the trade on error) ---
	commissionAmount := s.computeCommission(quoteAmount)
	if commissionAmount.Sign() > 0 && s.bankRecipient != nil {
		if cerr := exec.RunStep(ctx, "credit_commission", commissionAmount, order.ReservationCurrency,
			map[string]any{"memo_key": fmt.Sprintf("commission-%d", txn.ID)}, func() error {
				bankAcctNo, aerr := s.bankRecipient.BankCommissionAccountNumber(ctx)
				if aerr != nil {
					return aerr
				}
				commissionMemo := fmt.Sprintf("Commission for forex order #%d fill #%d", order.ID, txn.ID)
				_, ferr := s.accountClient.CreditAccount(ctx, bankAcctNo, commissionAmount, commissionMemo)
				return ferr
			}); cerr != nil {
			log.Printf("WARN: forex commission credit failed for order %d fill %d: %v (recovery will retry)",
				order.ID, txn.ID, cerr)
			// Deliberately do not return — the trade is valid.
		}
	}

	return nil
}

// compensateQuote reverse-credits the user's quote account for the amount
// that was debited during settle_reservation_quote. Used when a later step
// (base-account lookup or credit_base) fails after the debit already
// happened. Failure here is logged only: compensation is retried by the
// background saga recovery job if wired.
func (s *ForexFillService) compensateQuote(
	ctx context.Context,
	exec *SagaExecutor,
	order *model.Order,
	txn *model.OrderTransaction,
	forwardStepID uint64,
	quoteAmount decimal.Decimal,
) {
	_ = exec.RunCompensation(ctx, forwardStepID, "compensate_quote_settle", func() error {
		quoteAcct, gerr := s.accountClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: order.AccountID})
		if gerr != nil {
			return gerr
		}
		reverseMemo := fmt.Sprintf("Compensating forex order #%d fill #%d — refund quote", order.ID, txn.ID)
		_, cerr := s.accountClient.CreditAccount(ctx, quoteAcct.AccountNumber, quoteAmount, reverseMemo)
		return cerr
	})
}

// computeCommission mirrors PortfolioService.computeCommission so forex
// trades charge the same rate as stock/futures/option trades. Falls back
// to defaultCommissionRate (0.25%) when settings is nil.
func (s *ForexFillService) computeCommission(tradeValue decimal.Decimal) decimal.Decimal {
	rate := decimal.NewFromFloat(defaultCommissionRate)
	if s.settings != nil && s.settings.CommissionRate().Sign() > 0 {
		rate = s.settings.CommissionRate()
	}
	return tradeValue.Mul(rate)
}
