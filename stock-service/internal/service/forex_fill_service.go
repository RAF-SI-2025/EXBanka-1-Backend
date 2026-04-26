package service

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/shared"
	"github.com/exbanka/stock-service/internal/model"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
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
// Saga shape for a forex buy (driven by shared.Saga):
//
//	record_transaction          (audit only)
//	settle_reservation_quote    (PartialSettleReservation on quote account)
//	credit_base                 (CreditAccount on base account)
//
// On credit_base failure the executor walks back to settle_reservation_quote
// and runs its Backward (reverse-credit the quote account, refunding the
// user). The commission step runs AFTER the saga as best-effort — its
// failure must not unwind a valid trade.
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

	quoteMemo := fmt.Sprintf("Forex buy order #%d fill #%d — debit quote", order.ID, txn.ID)
	baseMemo := fmt.Sprintf("Forex buy order #%d fill #%d — credit base", order.ID, txn.ID)

	state := shared.NewState()
	state.Set("order_id", order.ID)
	state.Set("order_transaction_id", txn.ID)
	state.Set("step:record_transaction:amount", txn.TotalPrice)
	state.Set("step:record_transaction:currency", order.ReservationCurrency)
	state.Set("step:settle_reservation_quote:amount", quoteAmount)
	state.Set("step:settle_reservation_quote:currency", order.ReservationCurrency)
	state.Set("step:credit_base:amount", baseAmount)
	// credit_base's currency is set inside its Forward once we resolve baseAcct.

	saga := shared.NewSaga(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(shared.Step{
			Name: "record_transaction",
			Forward: func(ctx context.Context, _ *shared.State) error {
				// Audit-only step; the txn was already persisted above. The
				// row exists so saga recovery can replay from this point.
				return nil
			},
		}).
		Add(shared.Step{
			Name: "settle_reservation_quote",
			Forward: func(ctx context.Context, _ *shared.State) error {
				_, e := s.accountClient.PartialSettleReservation(ctx, order.ID, txn.ID, quoteAmount, quoteMemo)
				return e
			},
			Backward: func(ctx context.Context, _ *shared.State) error {
				// Reverse-credit the quote account so the user is made whole.
				quoteAcct, gerr := s.accountClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: order.AccountID})
				if gerr != nil {
					return gerr
				}
				reverseMemo := fmt.Sprintf("Compensating forex order #%d fill #%d — refund quote", order.ID, txn.ID)
				_, cerr := s.accountClient.CreditAccount(ctx, quoteAcct.AccountNumber, quoteAmount, reverseMemo, recoveryKeyFor("compensate_quote_settle", txn.ID))
				return cerr
			},
		}).
		Add(shared.Step{
			Name: "credit_base",
			Forward: func(ctx context.Context, st *shared.State) error {
				baseAcct, err := s.accountClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: *order.BaseAccountID})
				if err != nil {
					return fmt.Errorf("base account lookup: %w", err)
				}
				st.Set("step:credit_base:currency", baseAcct.CurrencyCode)
				_, cerr := s.accountClient.CreditAccount(ctx, baseAcct.AccountNumber, baseAmount, baseMemo, recoveryKeyFor("credit_base", txn.ID))
				return cerr
			},
			// Last money step in the saga. Nothing after, so no Backward.
		})

	if err := saga.Execute(ctx, state); err != nil {
		return err
	}

	// Best-effort commission credit. Failure here logs but does not unwind
	// the trade — it's recovered by the background saga recovery loop.
	commissionAmount := s.computeCommission(quoteAmount)
	if commissionAmount.Sign() > 0 && s.bankRecipient != nil {
		commissionMemo := fmt.Sprintf("Commission for forex order #%d fill #%d", order.ID, txn.ID)
		commSaga := shared.NewSaga(sagaID+"-comm", stocksaga.NewRecorder(s.sagaRepo)).
			Add(shared.Step{
				Name: "credit_commission",
				Forward: func(ctx context.Context, _ *shared.State) error {
					bankAcctNo, aerr := s.bankRecipient.BankCommissionAccountNumber(ctx)
					if aerr != nil {
						return aerr
					}
					_, ferr := s.accountClient.CreditAccount(ctx, bankAcctNo, commissionAmount, commissionMemo, recoveryKeyFor("credit_commission", txn.ID))
					return ferr
				},
			})
		commState := shared.NewState()
		commState.Set("order_id", order.ID)
		commState.Set("order_transaction_id", txn.ID)
		commState.Set("step:credit_commission:amount", commissionAmount)
		commState.Set("step:credit_commission:currency", order.ReservationCurrency)
		if cerr := commSaga.Execute(ctx, commState); cerr != nil {
			log.Printf("WARN: forex commission credit failed for order %d fill %d: %v (recovery will retry)",
				order.ID, txn.ID, cerr)
		}
	}

	return nil
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
