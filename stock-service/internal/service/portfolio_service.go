package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
)

// FillAccountClient is the subset of grpc.AccountClient the fill saga needs.
// Expressed as an interface so tests can stub it without the real wrapper.
// The fill saga calls:
//   - PartialSettleReservation to commit the held funds on buy fills,
//   - CreditAccount to credit the user (sell proceeds) or the bank commission
//     account, and to reverse-compensate on buy-side holding failures,
//   - DebitAccount to reverse-compensate a sell-side credit when the
//     holding-decrement step fails (Task 14),
//   - Stub() for direct GetAccount/UpdateBalance access on paths that predate
//     Phase 2 (stateAccountNo-based commission, legacy helpers, exercise-option).
type FillAccountClient interface {
	PartialSettleReservation(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*accountpb.PartialSettleReservationResponse, error)
	// CreditAccount and DebitAccount take an idempotencyKey (empty string
	// opts out). Callers driven by the fill/compensation saga pass a
	// deterministic key derived from the order-transaction ID so a retried
	// call after a crash becomes a safe no-op at the account-service layer.
	CreditAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error)
	DebitAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error)
	Stub() accountpb.AccountServiceClient
}

// FillHoldingReservationAPI is the narrow slice of HoldingReservationService
// the sell fill saga needs. Expressed as an interface so tests can stub
// PartialSettle without standing up a real DB + reservation ledger.
//
// PartialSettle is the quantity-side mirror of account-service's
// PartialSettleReservation: it decrements both Holding.ReservedQuantity and
// Holding.Quantity by `qty` under a SELECT FOR UPDATE transaction, and is
// idempotent on orderTransactionID via ON CONFLICT DO NOTHING on the
// settlements table.
type FillHoldingReservationAPI interface {
	PartialSettle(ctx context.Context, orderID, orderTransactionID uint64, qty int64) (*PartialSettleHoldingResult, error)
}

// FillSagaLogRepo extends SagaLogRepo with the lookup the fill saga needs
// for linking a compensation step to its forward (settle_reservation) step.
// The broader SagaLogRepo keeps a minimal surface to avoid ripple-updates to
// every service that uses it; this interface is only consumed here.
type FillSagaLogRepo interface {
	SagaLogRepo
	GetByStepName(orderID uint64, stepName string) (*model.SagaLog, error)
}

type PortfolioService struct {
	holdingRepo     HoldingRepo
	capitalGainRepo CapitalGainRepo
	listingRepo     ListingRepo
	stockRepo       StockRepo
	optionRepo      OptionRepo
	accountClient   accountpb.AccountServiceClient
	nameResolver    UserNameResolver
	stateAccountNo  string
	// Phase-2 fill-saga dependencies (Task 13 / Task 14). All optional: when
	// nil the service falls back to the legacy (pre-saga) behaviour so tests
	// that target ExerciseOption and legacy fill paths don't need to thread
	// them. The sell-fill saga additionally needs holdingReservationSvc.
	sagaRepo              FillSagaLogRepo
	txRepo                OrderTransactionRepo
	exchangeClient        exchangepb.ExchangeServiceClient
	fillClient            FillAccountClient
	holdingReservationSvc FillHoldingReservationAPI
	settings              OrderSettings
	// forexFillSvc handles forex buy fills (debit quote, credit base). When
	// nil, forex fills short-circuit with a warning — this matches the
	// pre-Task-15 behaviour and keeps legacy tests compiling without the
	// ForexFillService dependency.
	forexFillSvc *ForexFillService
}

// NewPortfolioService is the legacy constructor retained for existing call
// sites. It wires the pre-Phase-2 fields only; ProcessBuyFill will run in
// legacy mode (direct debit, no saga, no cross-currency conversion) unless
// the caller upgrades to NewPortfolioServiceWithFillSaga.
func NewPortfolioService(
	holdingRepo HoldingRepo,
	capitalGainRepo CapitalGainRepo,
	listingRepo ListingRepo,
	stockRepo StockRepo,
	optionRepo OptionRepo,
	accountClient accountpb.AccountServiceClient,
	nameResolver UserNameResolver,
	stateAccountNo string,
) *PortfolioService {
	return &PortfolioService{
		holdingRepo:     holdingRepo,
		capitalGainRepo: capitalGainRepo,
		listingRepo:     listingRepo,
		stockRepo:       stockRepo,
		optionRepo:      optionRepo,
		accountClient:   accountClient,
		nameResolver:    nameResolver,
		stateAccountNo:  stateAccountNo,
	}
}

// WithFillSaga returns a shallow copy of the receiver with the fill-saga
// dependencies populated. Call sites in cmd/main.go that have the saga
// repository, exchange client, fill client, holding-reservation service, and
// settings available use this to upgrade the service to the Phase-2
// ProcessBuyFill / ProcessSellFill paths. The holdingReservationSvc is only
// required by ProcessSellFill (Task 14); ProcessBuyFill ignores it.
func (s *PortfolioService) WithFillSaga(
	sagaRepo FillSagaLogRepo,
	txRepo OrderTransactionRepo,
	exchangeClient exchangepb.ExchangeServiceClient,
	fillClient FillAccountClient,
	holdingReservationSvc FillHoldingReservationAPI,
	settings OrderSettings,
) *PortfolioService {
	cp := *s
	cp.sagaRepo = sagaRepo
	cp.txRepo = txRepo
	cp.exchangeClient = exchangeClient
	cp.fillClient = fillClient
	cp.holdingReservationSvc = holdingReservationSvc
	cp.settings = settings
	return &cp
}

// WithForexFillService returns a shallow copy of the receiver with the
// forex-fill delegate populated. Separate from WithFillSaga because the
// forex path is independent of the stock/futures/options saga deps — it
// doesn't need exchangeClient or holdingReservationSvc — and because
// ForexFillService is constructed after PortfolioService in main.go.
func (s *PortfolioService) WithForexFillService(forexFillSvc *ForexFillService) *PortfolioService {
	cp := *s
	cp.forexFillSvc = forexFillSvc
	return &cp
}

// ProcessBuyFill handles a buy order fill for stocks / futures / options
// and routes forex fills to the ForexFillService when wired.
//
// Phase-2 path (when fill-saga deps are wired): runs a five-step saga —
// record_transaction → convert_amount → settle_reservation → update_holding →
// credit_commission. On update_holding failure the settle is reverse-credited
// via CreditAccount. Commission failure is logged and left for recovery so
// the trade remains valid.
//
// Forex buys delegate to ForexFillService (Task 15): settlement is a pure
// intra-user transfer (debit quote, credit base) with no holding row and no
// exchange-service call. When forexFillSvc is nil the fill short-circuits
// with a warning so legacy tests and callers that don't wire the service
// still compile and run.
//
// Legacy path (when sagaRepo is nil): falls back to the original direct-debit
// behaviour so tests and call sites that haven't upgraded to the saga path
// keep working.
func (s *PortfolioService) ProcessBuyFill(order *model.Order, txn *model.OrderTransaction) error {
	if order.SecurityType == "forex" {
		if s.forexFillSvc == nil {
			log.Printf("WARN: forex buy fill for order %d — forex_fill_service not wired", order.ID)
			return nil
		}
		return s.forexFillSvc.ProcessForexBuy(context.Background(), order, txn)
	}

	if s.sagaRepo == nil || s.fillClient == nil || s.txRepo == nil {
		return s.processBuyFillLegacy(order, txn)
	}
	return s.processBuyFillSaga(order, txn)
}

// processBuyFillSaga is the Phase-2 fill-saga implementation.
func (s *PortfolioService) processBuyFillSaga(order *model.Order, txn *model.OrderTransaction) error {
	ctx := context.Background()

	sagaID := order.SagaID
	if sagaID == "" {
		sagaID = uuid.New().String()
	}
	txnID := txn.ID
	exec := NewSagaExecutor(s.sagaRepo, sagaID, order.ID, &txnID)

	// --- Step 1: record_transaction (no-op; caller already persisted the txn) ---
	// The saga row gives recovery/reconciliation visibility over fills that
	// crash between commit and saga rows landing.
	if err := exec.RunStep(ctx, "record_transaction", txn.TotalPrice, order.ReservationCurrency, nil, func() error {
		return nil
	}); err != nil {
		return err
	}

	// --- Step 2: convert_amount ---
	var convertedAmount decimal.Decimal
	var accountCurrency string
	var listingCurrency string
	if err := exec.RunStep(ctx, "convert_amount", txn.TotalPrice, "", nil, func() error {
		listing, err := s.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return fmt.Errorf("listing lookup: %w", err)
		}
		listingCurrency = listing.Exchange.Currency

		acctCcy, err := s.accountCurrency(ctx, order.AccountID)
		if err != nil {
			return err
		}
		accountCurrency = acctCcy

		native := txn.TotalPrice
		txn.NativeAmount = &native
		txn.NativeCurrency = listingCurrency
		txn.AccountCurrency = accountCurrency

		if listingCurrency == accountCurrency || accountCurrency == "" {
			// Same currency (or legacy account with no currency code)
			// — no conversion required.
			convertedAmount = txn.TotalPrice
			c := convertedAmount
			txn.ConvertedAmount = &c
			return s.txRepo.Update(txn)
		}

		// Cross-currency: route through exchange-service.
		resp, cerr := s.exchangeClient.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: listingCurrency,
			ToCurrency:   accountCurrency,
			Amount:       txn.TotalPrice.String(),
		})
		if cerr != nil {
			return fmt.Errorf("exchange convert: %w", cerr)
		}
		conv, perr := decimal.NewFromString(resp.ConvertedAmount)
		if perr != nil {
			return fmt.Errorf("parse converted amount: %w", perr)
		}
		convertedAmount = conv
		c := conv
		txn.ConvertedAmount = &c
		if rate, rerr := decimal.NewFromString(resp.EffectiveRate); rerr == nil {
			r := rate
			txn.FxRate = &r
		}
		return s.txRepo.Update(txn)
	}); err != nil {
		return err
	}

	// --- Step 3: settle_reservation (idempotent on txn ID) ---
	memo := fmt.Sprintf("Order #%d partial fill (txn #%d)", order.ID, txn.ID)
	if err := exec.RunStep(ctx, "settle_reservation", convertedAmount, accountCurrency,
		map[string]any{"account_id": order.AccountID, "txn_id": txn.ID}, func() error {
			_, serr := s.fillClient.PartialSettleReservation(ctx, order.ID, txn.ID, convertedAmount, memo)
			return serr
		}); err != nil {
		return err
	}

	// --- Step 4: update_holding (idempotent on PK; weighted-average upsert) ---
	if err := exec.RunStep(ctx, "update_holding", decimal.Zero, "",
		map[string]any{"security_type": order.SecurityType, "ticker": order.Ticker}, func() error {
			return s.upsertHoldingForBuy(order, txn)
		}); err != nil {
		// Compensation: reverse-credit the settle amount to the user's account.
		var forwardID uint64
		if step, lerr := s.sagaRepo.GetByStepName(order.ID, "settle_reservation"); lerr == nil && step != nil {
			forwardID = step.ID
		}
		_ = exec.RunCompensation(ctx, forwardID, "compensate_settle_via_credit", func() error {
			reverseMemo := fmt.Sprintf("Compensating order #%d fill #%d", order.ID, txn.ID)
			acct, gerr := s.fillClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: order.AccountID})
			if gerr != nil {
				return gerr
			}
			_, cerr := s.fillClient.CreditAccount(ctx, acct.AccountNumber, convertedAmount, reverseMemo, recoveryKeyFor("compensate_settle_via_credit", txn.ID))
			return cerr
		})
		return err
	}

	// --- Step 5: credit_commission (best-effort; do NOT fail the trade on error) ---
	commissionAmount := s.computeCommission(convertedAmount)
	if commissionAmount.Sign() > 0 {
		if cerr := exec.RunStep(ctx, "credit_commission", commissionAmount, accountCurrency,
			map[string]any{"memo_key": fmt.Sprintf("commission-%d", txn.ID)}, func() error {
				// Route commission through the fill-saga AccountClient wrapper so
				// it shares the Phase-2 credit path (memo-aware, positive-only).
				// The deterministic idempotency key makes the saga recovery
				// retry path safe: if the credit already committed on a prior
				// attempt, a replay is a no-op on account-service.
				memo := fmt.Sprintf("Commission for order #%d fill #%d", order.ID, txn.ID)
				_, ferr := s.fillClient.CreditAccount(ctx, s.stateAccountNo, commissionAmount, memo, recoveryKeyFor("credit_commission", txn.ID))
				return ferr
			}); cerr != nil {
			log.Printf("WARN: commission credit failed for order %d fill %d: %v (recovery will retry)",
				order.ID, txn.ID, cerr)
			// Deliberately do not return — the trade is valid.
		}
	}

	return nil
}

// upsertHoldingForBuy materialises the Holding row for a buy fill. Called
// inside the update_holding saga step. Mirrors the legacy path (resolves
// first/last name via nameResolver, stock name via stockRepo when possible).
func (s *PortfolioService) upsertHoldingForBuy(order *model.Order, txn *model.OrderTransaction) error {
	firstName, lastName := "", ""
	if s.nameResolver != nil {
		if fn, ln, err := s.nameResolver(order.UserID, order.SystemType); err == nil {
			firstName, lastName = fn, ln
		}
	}

	listing, err := s.listingRepo.GetByID(order.ListingID)
	if err != nil {
		return err
	}

	securityName := order.Ticker
	if order.SecurityType == "stock" && s.stockRepo != nil {
		if stock, serr := s.stockRepo.GetByID(listing.SecurityID); serr == nil {
			securityName = stock.Name
		}
	}

	holding := &model.Holding{
		UserID:        order.UserID,
		SystemType:    order.SystemType,
		UserFirstName: firstName,
		UserLastName:  lastName,
		SecurityType:  order.SecurityType,
		SecurityID:    listing.SecurityID,
		ListingID:     order.ListingID,
		Ticker:        order.Ticker,
		Name:          securityName,
		Quantity:      txn.Quantity,
		AveragePrice:  txn.PricePerUnit,
		AccountID:     order.AccountID,
	}
	return s.holdingRepo.Upsert(holding)
}

// computeCommission returns the commission amount for a trade value, using
// the injected OrderSettings. Falls back to 0.25% when settings is nil
// (mirrors defaultOrderSettings) so the fill saga still collects commission
// even if the caller didn't wire settings through.
func (s *PortfolioService) computeCommission(tradeValue decimal.Decimal) decimal.Decimal {
	rate := decimal.NewFromFloat(defaultCommissionRate)
	if s.settings != nil && s.settings.CommissionRate().Sign() > 0 {
		rate = s.settings.CommissionRate()
	}
	return tradeValue.Mul(rate)
}

// accountCurrency resolves an account's currency_code via account-service.
// Empty string (not an error) when the stub isn't configured — the caller
// treats that as "same currency as listing" for graceful degradation.
func (s *PortfolioService) accountCurrency(ctx context.Context, accountID uint64) (string, error) {
	if s.fillClient == nil {
		return "", nil
	}
	stub := s.fillClient.Stub()
	if stub == nil {
		return "", nil
	}
	resp, err := stub.GetAccount(ctx, &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return "", fmt.Errorf("account lookup: %w", err)
	}
	return resp.GetCurrencyCode(), nil
}

// processBuyFillLegacy is the pre-Phase-2 direct-debit path retained for
// unit tests and call sites that haven't upgraded to the saga constructor.
// It mirrors the original ProcessBuyFill behaviour exactly.
func (s *PortfolioService) processBuyFillLegacy(order *model.Order, txn *model.OrderTransaction) error {
	// Look up user name for new holdings
	firstName, lastName := "", ""
	if s.nameResolver != nil {
		fn, ln, err := s.nameResolver(order.UserID, order.SystemType)
		if err == nil {
			firstName, lastName = fn, ln
		}
	}

	// Look up listing for security info
	listing, err := s.listingRepo.GetByID(order.ListingID)
	if err != nil {
		return err
	}

	// Look up security name
	securityName := order.Ticker
	if order.SecurityType == "stock" {
		if stock, err := s.stockRepo.GetByID(listing.SecurityID); err == nil {
			securityName = stock.Name
		}
	}

	holding := &model.Holding{
		UserID:        order.UserID,
		SystemType:    order.SystemType,
		UserFirstName: firstName,
		UserLastName:  lastName,
		SecurityType:  order.SecurityType,
		SecurityID:    listing.SecurityID,
		ListingID:     order.ListingID,
		Ticker:        order.Ticker,
		Name:          securityName,
		Quantity:      txn.Quantity,
		AveragePrice:  txn.PricePerUnit,
		AccountID:     order.AccountID,
	}

	if err := s.holdingRepo.Upsert(holding); err != nil {
		return err
	}

	// Debit buyer's account: total_price + proportional commission
	proportionalCommission := order.Commission.Mul(
		decimal.NewFromInt(txn.Quantity),
	).Div(decimal.NewFromInt(order.Quantity))
	debitAmount := txn.TotalPrice.Add(proportionalCommission)

	if err := s.debitAccount(order.AccountID, debitAmount); err != nil {
		return err
	}

	// Credit bank account with commission
	if err := s.creditBankCommission(proportionalCommission); err != nil {
		return err
	}

	return nil
}

// ProcessSellFill handles a sell order fill for stocks / futures / options.
//
// Phase-2 path (when fill-saga deps are wired): runs a mirror-image saga of
// ProcessBuyFill — record_transaction → convert_amount → credit_proceeds →
// decrement_holding → credit_commission. The ORDER MATTERS: the user is
// credited BEFORE their holding is decremented so a holding-decrement
// failure is compensated by reverse-debiting the credit (and the reservation
// itself stays active for the fill loop to retry).
//
// Forex sells short-circuit defensively — forex sells are rejected at
// placement (Task 12), so this branch is only reachable on a bug.
//
// Legacy path (when any fill-saga dep is nil): falls back to the original
// direct credit + holding-update behaviour so tests and call sites that
// haven't upgraded to the saga constructor keep working.
func (s *PortfolioService) ProcessSellFill(order *model.Order, txn *model.OrderTransaction) error {
	if order.SecurityType == "forex" {
		// Forex sells are rejected at placement (Task 12); reaching here
		// would indicate a placement-saga bug, not a legitimate fill.
		log.Printf("WARN: forex sell fill for order %d — forex sells are rejected at placement; no-op", order.ID)
		return nil
	}

	if s.sagaRepo == nil || s.fillClient == nil || s.txRepo == nil || s.holdingReservationSvc == nil {
		return s.processSellFillLegacy(order, txn)
	}
	return s.processSellFillSaga(order, txn)
}

// processSellFillSaga is the Phase-2 sell-fill saga implementation. Credits
// the seller first, then consumes the holding reservation; on
// holding-decrement failure the credit is reversed via DebitAccount.
func (s *PortfolioService) processSellFillSaga(order *model.Order, txn *model.OrderTransaction) error {
	ctx := context.Background()

	sagaID := order.SagaID
	if sagaID == "" {
		sagaID = uuid.New().String()
	}
	txnID := txn.ID
	exec := NewSagaExecutor(s.sagaRepo, sagaID, order.ID, &txnID)

	// --- Step 1: record_transaction (no-op; caller already persisted the txn) ---
	if err := exec.RunStep(ctx, "record_transaction", txn.TotalPrice, "", nil, func() error {
		return nil
	}); err != nil {
		return err
	}

	// --- Step 2: convert_amount ---
	var convertedAmount decimal.Decimal
	var accountCurrency, accountNumber, listingCurrency string
	var listing *model.Listing
	if err := exec.RunStep(ctx, "convert_amount", txn.TotalPrice, "", nil, func() error {
		l, err := s.listingRepo.GetByID(order.ListingID)
		if err != nil {
			return fmt.Errorf("listing lookup: %w", err)
		}
		listing = l
		listingCurrency = l.Exchange.Currency

		acct, err := s.fillClient.Stub().GetAccount(ctx, &accountpb.GetAccountRequest{Id: order.AccountID})
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		accountCurrency = acct.CurrencyCode
		accountNumber = acct.AccountNumber

		native := txn.TotalPrice
		txn.NativeAmount = &native
		txn.NativeCurrency = listingCurrency
		txn.AccountCurrency = accountCurrency

		if listingCurrency == accountCurrency || accountCurrency == "" {
			// Same currency (or legacy account with no currency code).
			convertedAmount = txn.TotalPrice
			c := convertedAmount
			txn.ConvertedAmount = &c
			return s.txRepo.Update(txn)
		}

		resp, cerr := s.exchangeClient.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: listingCurrency,
			ToCurrency:   accountCurrency,
			Amount:       txn.TotalPrice.String(),
		})
		if cerr != nil {
			return fmt.Errorf("exchange convert: %w", cerr)
		}
		conv, perr := decimal.NewFromString(resp.ConvertedAmount)
		if perr != nil {
			return fmt.Errorf("parse converted amount: %w", perr)
		}
		convertedAmount = conv
		c := conv
		txn.ConvertedAmount = &c
		if rate, rerr := decimal.NewFromString(resp.EffectiveRate); rerr == nil {
			r := rate
			txn.FxRate = &r
		}
		return s.txRepo.Update(txn)
	}); err != nil {
		return err
	}

	// --- Step 3: credit_proceeds ---
	creditMemo := fmt.Sprintf("Sell fill for order #%d (txn #%d)", order.ID, txn.ID)
	if err := exec.RunStep(ctx, "credit_proceeds", convertedAmount, accountCurrency,
		map[string]any{"account_id": order.AccountID, "txn_id": txn.ID}, func() error {
			_, cerr := s.fillClient.CreditAccount(ctx, accountNumber, convertedAmount, creditMemo, recoveryKeyFor("credit_proceeds", txn.ID))
			return cerr
		}); err != nil {
		return err
	}

	// --- Step 4: decrement_holding (via HoldingReservationService.PartialSettle) ---
	// PartialSettle is idempotent on orderTransactionID and atomically
	// decrements both Holding.ReservedQuantity and Holding.Quantity. On
	// failure we compensate by reverse-debiting the credit_proceeds amount.
	if err := exec.RunStep(ctx, "decrement_holding", decimal.NewFromInt(txn.Quantity), "",
		map[string]any{"security_type": order.SecurityType, "ticker": order.Ticker}, func() error {
			_, perr := s.holdingReservationSvc.PartialSettle(ctx, order.ID, txn.ID, txn.Quantity)
			if perr != nil {
				return perr
			}
			// Capital gain is part of the logical holding transition, so
			// record it inside the same saga step. A failure here is
			// treated the same as PartialSettle failure — the credit
			// must be reversed.
			return s.recordCapitalGain(order, txn, listing)
		}); err != nil {
		// Compensate: reverse the credit via DebitAccount.
		var forwardID uint64
		if step, lerr := s.sagaRepo.GetByStepName(order.ID, "credit_proceeds"); lerr == nil && step != nil {
			forwardID = step.ID
		}
		_ = exec.RunCompensation(ctx, forwardID, "compensate_credit_via_debit", func() error {
			reverseMemo := fmt.Sprintf("Compensating sell order #%d fill #%d", order.ID, txn.ID)
			_, derr := s.fillClient.DebitAccount(ctx, accountNumber, convertedAmount, reverseMemo, recoveryKeyFor("compensate_credit_via_debit", txn.ID))
			return derr
		})
		return err
	}

	// --- Step 5: credit_commission (best-effort; do NOT fail the trade on error) ---
	commissionAmount := s.computeCommission(convertedAmount)
	if commissionAmount.Sign() > 0 {
		if cerr := exec.RunStep(ctx, "credit_commission", commissionAmount, accountCurrency,
			map[string]any{"memo_key": fmt.Sprintf("commission-%d", txn.ID)}, func() error {
				commissionMemo := fmt.Sprintf("Commission for order #%d fill #%d", order.ID, txn.ID)
				_, ferr := s.fillClient.CreditAccount(ctx, s.stateAccountNo, commissionAmount, commissionMemo, recoveryKeyFor("credit_commission", txn.ID))
				return ferr
			}); cerr != nil {
			log.Printf("WARN: commission credit failed for sell order %d fill %d: %v (recovery will retry)",
				order.ID, txn.ID, cerr)
			// Deliberately do not return — the trade is valid.
		}
	}

	return nil
}

// recordCapitalGain persists a CapitalGain row for a sell fill. Called
// inside the decrement_holding saga step (and by the legacy path) so gain
// recording is atomic with the holding transition from the caller's POV.
// Looks up the pre-fill AveragePrice on the holding via the repo; the
// average_price on a sell is the cost basis so it does not change as shares
// leave the position.
func (s *PortfolioService) recordCapitalGain(order *model.Order, txn *model.OrderTransaction, listing *model.Listing) error {
	holding, err := s.holdingRepo.GetByUserAndSecurity(
		order.UserID, order.SystemType, order.SecurityType, listing.SecurityID,
	)
	if err != nil {
		// PartialSettle deletes the row when Quantity hits zero; in that
		// case we can't look it up here. A missing holding after a
		// successful PartialSettle is benign — capital gain is best-
		// effort at the saga layer. Log and continue.
		log.Printf("WARN: capital gain skipped for order %d fill %d (holding not found after partial settle): %v",
			order.ID, txn.ID, err)
		return nil
	}
	currency := ""
	if listing != nil {
		currency = listing.Exchange.Currency
	}
	gain := txn.PricePerUnit.Sub(holding.AveragePrice).Mul(decimal.NewFromInt(txn.Quantity))
	capitalGain := &model.CapitalGain{
		UserID:             order.UserID,
		SystemType:         order.SystemType,
		OrderTransactionID: txn.ID,
		OTC:                false,
		SecurityType:       order.SecurityType,
		Ticker:             order.Ticker,
		Quantity:           txn.Quantity,
		BuyPricePerUnit:    holding.AveragePrice,
		SellPricePerUnit:   txn.PricePerUnit,
		TotalGain:          gain,
		Currency:           currency,
		AccountID:          order.AccountID,
		TaxYear:            time.Now().Year(),
		TaxMonth:           int(time.Now().Month()),
	}
	return s.capitalGainRepo.Create(capitalGain)
}

// processSellFillLegacy is the pre-Phase-2 direct-credit path retained for
// unit tests and call sites that haven't upgraded to the saga constructor.
// It mirrors the original ProcessSellFill behaviour exactly.
func (s *PortfolioService) processSellFillLegacy(order *model.Order, txn *model.OrderTransaction) error {
	listing, err := s.listingRepo.GetByID(order.ListingID)
	if err != nil {
		return err
	}

	holding, err := s.holdingRepo.GetByUserAndSecurity(
		order.UserID, order.SystemType, order.SecurityType, listing.SecurityID,
	)
	if err != nil {
		return errors.New("holding not found for sell order")
	}

	if holding.Quantity < txn.Quantity {
		return errors.New("insufficient holding quantity for sell")
	}

	gain := txn.PricePerUnit.Sub(holding.AveragePrice).Mul(decimal.NewFromInt(txn.Quantity))
	capitalGain := &model.CapitalGain{
		UserID:             order.UserID,
		SystemType:         order.SystemType,
		OrderTransactionID: txn.ID,
		OTC:                false,
		SecurityType:       order.SecurityType,
		Ticker:             order.Ticker,
		Quantity:           txn.Quantity,
		BuyPricePerUnit:    holding.AveragePrice,
		SellPricePerUnit:   txn.PricePerUnit,
		TotalGain:          gain,
		Currency:           listing.Exchange.Currency,
		AccountID:          order.AccountID,
		TaxYear:            time.Now().Year(),
		TaxMonth:           int(time.Now().Month()),
	}
	if err := s.capitalGainRepo.Create(capitalGain); err != nil {
		return err
	}

	holding.Quantity -= txn.Quantity
	if holding.PublicQuantity > holding.Quantity {
		holding.PublicQuantity = holding.Quantity
	}

	if holding.Quantity == 0 {
		if err := s.holdingRepo.Delete(holding.ID); err != nil {
			return err
		}
	} else {
		if err := s.holdingRepo.Update(holding); err != nil {
			return err
		}
	}

	proportionalCommission := order.Commission.Mul(
		decimal.NewFromInt(txn.Quantity),
	).Div(decimal.NewFromInt(order.Quantity))
	creditAmount := txn.TotalPrice.Sub(proportionalCommission)

	if err := s.creditAccount(order.AccountID, creditAmount); err != nil {
		return err
	}

	return s.creditBankCommission(proportionalCommission)
}

// ListHoldings returns a (user_id, system_type) owner's holdings with
// computed profit.
func (s *PortfolioService) ListHoldings(userID uint64, systemType string, filter HoldingFilter) ([]model.Holding, int64, error) {
	return s.holdingRepo.ListByUser(userID, systemType, filter)
}

// GetCurrentPrice retrieves current listing price for a holding.
func (s *PortfolioService) GetCurrentPrice(listingID uint64) (decimal.Decimal, error) {
	listing, err := s.listingRepo.GetByID(listingID)
	if err != nil {
		return decimal.Zero, err
	}
	return listing.Price, nil
}

// MakePublic sets a number of shares as publicly available for OTC trading.
// Ownership is enforced on (user_id, system_type) so cross-system ID collisions
// cannot mutate another owner's holding.
func (s *PortfolioService) MakePublic(holdingID, userID uint64, systemType string, quantity int64) (*model.Holding, error) {
	holding, err := s.holdingRepo.GetByID(holdingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("holding not found")
		}
		return nil, err
	}
	if holding.UserID != userID || holding.SystemType != systemType {
		return nil, errors.New("holding does not belong to user")
	}
	if holding.SecurityType != "stock" {
		return nil, errors.New("only stocks can be made public for OTC trading")
	}
	if quantity < 0 || quantity > holding.Quantity {
		return nil, errors.New("invalid public quantity")
	}

	holding.PublicQuantity = quantity

	if err := s.holdingRepo.Update(holding); err != nil {
		return nil, err
	}
	return holding, nil
}

// ExerciseOption exercises an option holding if it's in the money and not
// expired. Ownership is enforced on (user_id, system_type) so cross-system ID
// collisions cannot exercise another owner's option.
func (s *PortfolioService) ExerciseOption(holdingID, userID uint64, systemType string) (*ExerciseResult, error) {
	holding, err := s.holdingRepo.GetByID(holdingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("holding not found")
		}
		return nil, err
	}
	if holding.UserID != userID || holding.SystemType != systemType {
		return nil, errors.New("holding does not belong to user")
	}
	if holding.SecurityType != "option" {
		return nil, errors.New("holding is not an option")
	}

	// Look up the option to get strike, type, settlement, stock info
	option, err := s.optionRepo.GetByID(holding.SecurityID)
	if err != nil {
		return nil, errors.New("option not found")
	}

	// Check settlement date hasn't passed
	if time.Now().After(option.SettlementDate) {
		return nil, errors.New("option has expired (settlement date passed)")
	}

	// Look up current stock price via listing
	stockListing, err := s.listingRepo.GetBySecurityIDAndType(option.StockID, "stock")
	if err != nil {
		return nil, errors.New("stock listing not found for option's underlying")
	}

	sharesAffected := holding.Quantity * 100 // 1 option = 100 shares
	var profit decimal.Decimal

	if option.OptionType == "call" {
		// CALL: stock price must be > strike price
		if stockListing.Price.LessThanOrEqual(option.StrikePrice) {
			return nil, errors.New("call option is not in the money")
		}
		profit = stockListing.Price.Sub(option.StrikePrice).Mul(decimal.NewFromInt(sharesAffected))

		// Debit account by strike × shares (buying stock at strike price)
		debitAmount := option.StrikePrice.Mul(decimal.NewFromInt(sharesAffected))
		if err := s.debitAccount(holding.AccountID, debitAmount); err != nil {
			return nil, err
		}

		// Create/update stock holding at strike price
		stock, err := s.stockRepo.GetByID(option.StockID)
		if err != nil {
			// Compensate: re-credit the debit amount
			_ = s.creditAccount(holding.AccountID, debitAmount)
			return nil, err
		}
		stockHolding := &model.Holding{
			UserID:        userID,
			SystemType:    holding.SystemType,
			UserFirstName: holding.UserFirstName,
			UserLastName:  holding.UserLastName,
			SecurityType:  "stock",
			SecurityID:    option.StockID,
			ListingID:     stockListing.ID,
			Ticker:        stock.Ticker,
			Name:          stock.Name,
			Quantity:      sharesAffected,
			AveragePrice:  option.StrikePrice,
			AccountID:     holding.AccountID,
		}
		if err := s.holdingRepo.Upsert(stockHolding); err != nil {
			// Compensate: re-credit the debit amount
			_ = s.creditAccount(holding.AccountID, debitAmount)
			return nil, err
		}

	} else { // "put"
		// PUT: stock price must be < strike price
		if stockListing.Price.GreaterThanOrEqual(option.StrikePrice) {
			return nil, errors.New("put option is not in the money")
		}
		profit = option.StrikePrice.Sub(stockListing.Price).Mul(decimal.NewFromInt(sharesAffected))

		// User must hold enough stock
		stock, err := s.stockRepo.GetByID(option.StockID)
		if err != nil {
			return nil, err
		}
		stockHolding, err := s.holdingRepo.GetByUserAndSecurity(userID, holding.SystemType, "stock", option.StockID)
		if err != nil || stockHolding.Quantity < sharesAffected {
			return nil, errors.New("insufficient stock holdings to exercise put option")
		}

		// Credit account by strike × shares (selling stock at strike price)
		creditAmount := option.StrikePrice.Mul(decimal.NewFromInt(sharesAffected))
		if err := s.creditAccount(holding.AccountID, creditAmount); err != nil {
			return nil, err
		}

		// Record the average price before modifying stockHolding for capital gain calculation
		avgPriceBeforeUpdate := stockHolding.AveragePrice

		// Decrease stock holding
		stockHolding.Quantity -= sharesAffected
		if stockHolding.PublicQuantity > stockHolding.Quantity {
			stockHolding.PublicQuantity = stockHolding.Quantity
		}

		if stockHolding.Quantity == 0 {
			if err := s.holdingRepo.Delete(stockHolding.ID); err != nil {
				// Compensate: re-debit the credit amount
				_ = s.debitAccount(holding.AccountID, creditAmount)
				return nil, err
			}
		} else {
			if err := s.holdingRepo.Update(stockHolding); err != nil {
				// Compensate: re-debit the credit amount
				_ = s.debitAccount(holding.AccountID, creditAmount)
				return nil, err
			}
		}

		// Record capital gain for the stock sale
		gain := option.StrikePrice.Sub(avgPriceBeforeUpdate).Mul(decimal.NewFromInt(sharesAffected))
		capitalGain := &model.CapitalGain{
			UserID:           userID,
			SystemType:       holding.SystemType,
			SecurityType:     "stock",
			Ticker:           stock.Ticker,
			Quantity:         sharesAffected,
			BuyPricePerUnit:  avgPriceBeforeUpdate,
			SellPricePerUnit: option.StrikePrice,
			TotalGain:        gain,
			Currency:         stockListing.Exchange.Currency,
			AccountID:        holding.AccountID,
			TaxYear:          time.Now().Year(),
			TaxMonth:         int(time.Now().Month()),
		}
		if err := s.capitalGainRepo.Create(capitalGain); err != nil {
			// Compensate: re-debit the credit amount
			_ = s.debitAccount(holding.AccountID, creditAmount)
			return nil, err
		}
	}

	// Delete option holding
	if err := s.holdingRepo.Delete(holdingID); err != nil {
		return nil, err
	}

	return &ExerciseResult{
		ID:                holdingID,
		OptionTicker:      holding.Ticker,
		ExercisedQuantity: holding.Quantity,
		SharesAffected:    sharesAffected,
		Profit:            profit,
	}, nil
}

// ExerciseOptionByOptionID exercises an option identified by its option_id.
// When holdingID > 0, the specified holding is exercised directly (delegates
// to ExerciseOption). When holdingID == 0, the (user_id, system_type) owner's
// oldest long holding for the given optionID is auto-resolved and exercised.
func (s *PortfolioService) ExerciseOptionByOptionID(ctx context.Context, optionID, userID uint64, systemType string, holdingID uint64) (*ExerciseResult, error) {
	if holdingID > 0 {
		return s.ExerciseOption(holdingID, userID, systemType)
	}

	// Auto-resolve: find the owner's oldest long holding on this option.
	holding, err := s.holdingRepo.FindOldestLongOptionHolding(userID, systemType, optionID)
	if err != nil {
		return nil, err
	}
	if holding == nil {
		return nil, errors.New("option holding not found")
	}
	return s.ExerciseOption(holding.ID, userID, systemType)
}

// --- Account helpers ---

func (s *PortfolioService) debitAccount(accountID uint64, amount decimal.Decimal) error {
	acctResp, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return err
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   acctResp.AccountNumber,
		Amount:          amount.Neg().StringFixed(4), // negative for debit
		UpdateAvailable: true,
	})
	return err
}

func (s *PortfolioService) creditAccount(accountID uint64, amount decimal.Decimal) error {
	acctResp, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return err
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   acctResp.AccountNumber,
		Amount:          amount.StringFixed(4), // positive for credit
		UpdateAvailable: true,
	})
	return err
}

func (s *PortfolioService) creditBankCommission(commission decimal.Decimal) error {
	if commission.IsZero() {
		return nil
	}
	_, err := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   s.stateAccountNo,
		Amount:          commission.StringFixed(4),
		UpdateAvailable: true,
	})
	return err
}

// --- Types ---

type ExerciseResult struct {
	ID                uint64
	OptionTicker      string
	ExercisedQuantity int64
	SharesAffected    int64
	Profit            decimal.Decimal
}
