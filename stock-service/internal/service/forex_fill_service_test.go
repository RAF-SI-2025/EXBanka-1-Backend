package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// Helpers — reuse the existing mocks from portfolio_service_test.go
// (mockFillAccountClient, mockSagaRepo, mockOrderTxRepo, mockAccountClient).
// ---------------------------------------------------------------------------

// fakeBankRecipient is a minimal BankCommissionRecipient stub for the
// forex fill tests. Returns a fixed bank account number and never fails.
type fakeBankRecipient struct {
	accountNo string
	err       error
}

func (f fakeBankRecipient) BankCommissionAccountNumber(context.Context) (string, error) {
	return f.accountNo, f.err
}

// forexFillMocks bundles the dependencies the forex fill tests inspect.
type forexFillMocks struct {
	sagaRepo      *mockSagaRepo
	txRepo        *mockOrderTxRepo
	accountClient *mockAccountClient
	fillClient    *mockFillAccountClient
	bankRecipient fakeBankRecipient
}

// buildForexFillService builds a ForexFillService with mock deps. The
// bank-commission account is registered under ID=0 on the stub so the
// existing mockFillAccountClient routes commission credits into its
// dedicated slice (matching the buy/sell saga tests).
func buildForexFillService() (*ForexFillService, *forexFillMocks) {
	accountClient := newMockAccountClient()
	// Quote account (USD) — the reservation was made here.
	accountClient.addAccount(77, "QUOTE-USD-77")
	accountClient.accounts[77].CurrencyCode = "USD"
	// Base account (EUR) — the user will receive base currency here.
	accountClient.addAccount(88, "BASE-EUR-88")
	accountClient.accounts[88].CurrencyCode = "EUR"
	// State account under ID=0 so the mock routes commission credits aside.
	accountClient.addAccount(0, "STATE-ACCT-001")
	accountClient.accounts[0].CurrencyCode = "USD"

	fillClient := newMockFillAccountClient(accountClient)
	sagaRepo := newMockSagaRepo()
	txRepo := newMockOrderTxRepo()
	bankRecipient := fakeBankRecipient{accountNo: "STATE-ACCT-001"}

	svc := NewForexFillService(sagaRepo, fillClient, txRepo, nil, bankRecipient)

	return svc, &forexFillMocks{
		sagaRepo:      sagaRepo,
		txRepo:        txRepo,
		accountClient: accountClient,
		fillClient:    fillClient,
		bankRecipient: bankRecipient,
	}
}

// forexOrder builds an EUR/USD forex buy order with quote=77, base=88.
func forexOrder(orderID uint64) *model.Order {
	baseID := uint64(88)
	uid := uint64(42)
	return &model.Order{
		ID:                  orderID,
		OwnerType:           model.OwnerClient,
		OwnerID:             &uid,
		ListingID:           1,
		SecurityType:        "forex",
		Ticker:              "EUR/USD",
		Direction:           "buy",
		Quantity:            100,
		ContractSize:        1000,
		AccountID:           77, // quote USD account
		BaseAccountID:       &baseID,
		ReservationCurrency: "USD",
		SagaID:              "test-forex-saga",
	}
}

// forexTxn builds an OrderTransaction for a fill of the given lot size at
// the given per-unit rate. For EUR/USD at rate 1.05 on qty=100 contracts of
// size 1000: txn.TotalPrice = 100 × 1.05 × 1000 = 105000 USD; the service
// computes baseAmount = 100 × 1000 = 100000 EUR.
func forexTxn(txnID, orderID uint64, qty int64, ratePerUnit float64, contractSize int64) *model.OrderTransaction {
	per := decimal.NewFromFloat(ratePerUnit)
	total := per.Mul(decimal.NewFromInt(qty)).Mul(decimal.NewFromInt(contractSize))
	return &model.OrderTransaction{
		ID:           txnID,
		OrderID:      orderID,
		Quantity:     qty,
		PricePerUnit: per,
		TotalPrice:   total,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestProcessForexBuy_DebitsQuoteCreditsBase_NoHolding(t *testing.T) {
	svc, mocks := buildForexFillService()

	// EUR/USD at 1.05 on qty=100 lots × contract_size=1000:
	//   quoteAmount = 100 × 1.05 × 1000 = 105000 USD
	//   baseAmount  = 100 × 1000 = 100000 EUR
	order := forexOrder(1)
	txn := forexTxn(900, 1, 100, 1.05, 1000)
	if err := mocks.txRepo.Create(txn); err != nil {
		t.Fatalf("failed to create txn: %v", err)
	}

	if err := svc.ProcessForexBuy(context.Background(), order, txn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --- settle_reservation_quote: PartialSettleReservation on the quote account ---
	if len(mocks.fillClient.partialSettleCalls) != 1 {
		t.Fatalf("expected 1 PartialSettleReservation call, got %d", len(mocks.fillClient.partialSettleCalls))
	}
	ps := mocks.fillClient.partialSettleCalls[0]
	if ps.OrderID != 1 || ps.OrderTransactionID != 900 {
		t.Errorf("settle IDs: got order=%d txn=%d want 1/900", ps.OrderID, ps.OrderTransactionID)
	}
	if !ps.Amount.Equal(decimal.NewFromInt(105_000)) {
		t.Errorf("settle amount: got %s want 105000", ps.Amount)
	}

	// --- credit_base: CreditAccount on the base (EUR) account with 100000 ---
	// mockFillAccountClient routes non-state credits into creditAccountCalls.
	if len(mocks.fillClient.creditAccountCalls) != 1 {
		t.Fatalf("expected 1 credit_base call, got %d", len(mocks.fillClient.creditAccountCalls))
	}
	base := mocks.fillClient.creditAccountCalls[0]
	if base.AccountNumber != "BASE-EUR-88" {
		t.Errorf("base credit target: got %s want BASE-EUR-88", base.AccountNumber)
	}
	if !base.Amount.Equal(decimal.NewFromInt(100_000)) {
		t.Errorf("base credit amount: got %s want 100000", base.Amount)
	}

	// --- credit_commission: routed to state account under ID=0. ---
	if len(mocks.fillClient.commissionCalls) != 1 {
		t.Fatalf("expected 1 commission credit, got %d", len(mocks.fillClient.commissionCalls))
	}
	if mocks.fillClient.commissionCalls[0].AccountNumber != "STATE-ACCT-001" {
		t.Errorf("commission account: got %s want STATE-ACCT-001", mocks.fillClient.commissionCalls[0].AccountNumber)
	}
	// Commission on quote: 105000 × 0.0025 = 262.5
	wantCommission := decimal.NewFromInt(105_000).Mul(decimal.NewFromFloat(defaultCommissionRate))
	if !mocks.fillClient.commissionCalls[0].Amount.Equal(wantCommission) {
		t.Errorf("commission amount: got %s want %s",
			mocks.fillClient.commissionCalls[0].Amount, wantCommission)
	}

	// --- No exchange-service conversion: the mockFillAccountClient doesn't
	// wrap an exchange client, but we assert via txn.FxRate staying nil. ---
	if txn.FxRate != nil {
		t.Errorf("FxRate should be nil for forex (no exchange-service call), got %v", txn.FxRate)
	}

	// --- txn audit fields should mirror quote amount in both slots. ---
	if txn.NativeAmount == nil || !txn.NativeAmount.Equal(decimal.NewFromInt(105_000)) {
		t.Errorf("NativeAmount: got %v want 105000", txn.NativeAmount)
	}
	if txn.ConvertedAmount == nil || !txn.ConvertedAmount.Equal(decimal.NewFromInt(105_000)) {
		t.Errorf("ConvertedAmount: got %v want 105000 (same as native for forex)", txn.ConvertedAmount)
	}
	if txn.NativeCurrency != "USD" || txn.AccountCurrency != "USD" {
		t.Errorf("currency fields: native=%s account=%s; want both USD",
			txn.NativeCurrency, txn.AccountCurrency)
	}

	// --- Saga log: record_transaction, settle_reservation_quote, credit_base, credit_commission ---
	// Every step should be recorded (pending → completed).
	stepNames := make(map[string]int)
	for _, r := range mocks.sagaRepo.rows {
		stepNames[r.StepName]++
	}
	for _, want := range []string{"record_transaction", "settle_reservation_quote", "credit_base", "credit_commission"} {
		if stepNames[want] != 1 {
			t.Errorf("saga step %q: got %d rows, want 1 (all steps: %v)", want, stepNames[want], stepNames)
		}
	}
}

func TestProcessForexBuy_BaseCreditFails_CompensatesQuote(t *testing.T) {
	svc, mocks := buildForexFillService()

	order := forexOrder(2)
	txn := forexTxn(901, 2, 100, 1.05, 1000)
	if err := mocks.txRepo.Create(txn); err != nil {
		t.Fatalf("failed to create txn: %v", err)
	}

	// Force credit_base (CreditAccount on non-state account) to fail.
	mocks.fillClient.creditErr = errors.New("base credit blew up")

	err := svc.ProcessForexBuy(context.Background(), order, txn)
	if err == nil {
		t.Fatal("expected error when base credit fails")
	}

	// Quote was debited before base-credit attempt.
	if len(mocks.fillClient.partialSettleCalls) != 1 {
		t.Fatalf("expected 1 quote settle before failure, got %d", len(mocks.fillClient.partialSettleCalls))
	}

	// Compensation: reverse-credit on the quote account for 105000. The
	// mock routes non-state credits into creditAccountCalls and clears the
	// creditErr on each call (but our mock does NOT auto-clear, so we
	// expect zero successful credit calls for the forward credit_base,
	// and the compensation call must also route through CreditAccount.
	// The compensation re-enables after we clear creditErr below — but
	// the service shouldn't need us to clear it: the compensation path
	// re-uses the same CreditAccount entry point.
	//
	// Strategy: verify exactly one saga row with step_name=credit_base
	// marked failed, and a compensation row for compensate_quote_settle.
	// We don't assert on creditAccountCalls because creditErr prevents
	// recording; instead, clear the error first and retry? No — the
	// production code calls CreditAccount for compensation which WILL
	// also hit creditErr. So we clear creditErr BEFORE the test runs
	// for the compensation call path... but we can't (we're asserting
	// the forward credit fails).
	//
	// Better: assert the saga rows reflect the compensation attempt
	// (recorded as pending then failed). The compensation call fails
	// the same way but that's OK — the compensation is best-effort and
	// the test in question only verifies "compensation was attempted".
	// shared.Saga writes the compensation row with step_name matching the
	// forward step (settle_reservation_quote) and IsCompensation=true.
	var compRow *model.SagaLog
	for _, r := range mocks.sagaRepo.rows {
		if r.StepName == "settle_reservation_quote" && r.IsCompensation {
			compRow = r
			break
		}
	}
	if compRow == nil {
		t.Fatal("expected a compensation row for settle_reservation_quote after base credit failure")
	}
}

func TestProcessForexBuy_BaseCreditFails_CompensationLinksForwardStep(t *testing.T) {
	// Regression test for saga-row integrity: when credit_base fails, the
	// compensation row must be recorded with CompensationOf pointing at the
	// settle_reservation_quote forward step.
	svc, mocks := buildForexFillService()

	order := forexOrder(3)
	txn := forexTxn(902, 3, 100, 1.05, 1000)
	_ = mocks.txRepo.Create(txn)

	mocks.fillClient.creditErr = errors.New("base credit down")

	err := svc.ProcessForexBuy(context.Background(), order, txn)
	if err == nil {
		t.Fatal("expected error when credit_base fails")
	}

	// shared.Saga writes the compensation row with step_name matching the
	// forward step (settle_reservation_quote) and IsCompensation=true.
	// settleRow is the forward (IsCompensation=false), compRow is the
	// reverse (IsCompensation=true).
	var baseRow, compRow, settleRow *model.SagaLog
	for _, r := range mocks.sagaRepo.rows {
		switch r.StepName {
		case "credit_base":
			baseRow = r
		case "settle_reservation_quote":
			if r.IsCompensation {
				compRow = r
			} else {
				settleRow = r
			}
		}
	}
	if baseRow == nil || baseRow.Status != model.SagaStatusFailed {
		t.Errorf("credit_base saga row status: got %+v, want failed", baseRow)
	}
	if settleRow == nil {
		t.Fatal("missing forward settle_reservation_quote saga row")
	}
	if compRow == nil {
		t.Fatal("missing compensation row for settle_reservation_quote")
	}
	if compRow.CompensationOf == nil || *compRow.CompensationOf != settleRow.ID {
		t.Errorf("compensation row must link to settle_reservation_quote (id=%d), got CompensationOf=%v",
			settleRow.ID, compRow.CompensationOf)
	}
}

func TestProcessForexBuy_MissingBaseAccount_ReturnsError(t *testing.T) {
	svc, mocks := buildForexFillService()

	order := forexOrder(4)
	order.BaseAccountID = nil // break the invariant
	txn := forexTxn(903, 4, 100, 1.05, 1000)

	err := svc.ProcessForexBuy(context.Background(), order, txn)
	if err == nil {
		t.Fatal("expected error when BaseAccountID is nil")
	}

	// No settlement should have been attempted.
	if len(mocks.fillClient.partialSettleCalls) != 0 {
		t.Errorf("no settle expected for invalid order, got %d calls", len(mocks.fillClient.partialSettleCalls))
	}
	if len(mocks.fillClient.creditAccountCalls) != 0 {
		t.Errorf("no credit expected for invalid order, got %d calls", len(mocks.fillClient.creditAccountCalls))
	}
	if len(mocks.sagaRepo.rows) != 0 {
		t.Errorf("no saga rows expected for invalid order, got %d", len(mocks.sagaRepo.rows))
	}
}

func TestProcessForexBuy_NonForexSecurity_ReturnsError(t *testing.T) {
	svc, _ := buildForexFillService()

	order := forexOrder(5)
	order.SecurityType = "stock" // misrouted
	txn := forexTxn(904, 5, 100, 1.05, 1000)

	err := svc.ProcessForexBuy(context.Background(), order, txn)
	if err == nil {
		t.Fatal("expected error when security_type is not forex")
	}
}

func TestProcessForexBuy_CommissionFails_TradeStillSucceeds(t *testing.T) {
	svc, mocks := buildForexFillService()

	order := forexOrder(6)
	txn := forexTxn(905, 6, 100, 1.05, 1000)
	_ = mocks.txRepo.Create(txn)

	// Route commission failure ONLY (the mock isolates state-account credits
	// into commissionErr, so credit_base still succeeds).
	mocks.fillClient.commissionErr = errors.New("state account unreachable")

	if err := svc.ProcessForexBuy(context.Background(), order, txn); err != nil {
		t.Fatalf("commission failure should not fail the trade: %v", err)
	}

	// Settle + base credit still done.
	if len(mocks.fillClient.partialSettleCalls) != 1 {
		t.Errorf("settle should still happen, got %d", len(mocks.fillClient.partialSettleCalls))
	}
	if len(mocks.fillClient.creditAccountCalls) != 1 {
		t.Errorf("base credit should still happen, got %d", len(mocks.fillClient.creditAccountCalls))
	}

	// Commission was NOT recorded (mock's commissionErr short-circuits before append).
	if len(mocks.fillClient.commissionCalls) != 0 {
		t.Errorf("commission credit should be absent on failure, got %d", len(mocks.fillClient.commissionCalls))
	}
}

// ---------------------------------------------------------------------------
// Integration into ProcessBuyFill: forex orders should route to the
// ForexFillService when wired.
// ---------------------------------------------------------------------------

func TestProcessBuyFill_RoutesForexToForexService(t *testing.T) {
	// Build the fill-saga-wired PortfolioService used by existing tests.
	svc, mocks := buildPortfolioServiceWithSaga("USD", "USD")

	// Build a real ForexFillService on top of the same mocks so the saga
	// settlement actually fires. The existing mockFillAccountClient in
	// fillSagaMocks already stubs out all the gRPC calls the forex saga
	// needs (PartialSettleReservation, CreditAccount, GetAccount via the
	// inner mockAccountClient).
	mocks.accountClient.addAccount(88, "BASE-EUR-88")
	mocks.accountClient.accounts[88].CurrencyCode = "EUR"

	forexSvc := NewForexFillService(
		mocks.sagaRepo, mocks.fillClient, mocks.txRepo, nil,
		fakeBankRecipient{accountNo: "STATE-ACCT-001"},
	)
	svc = svc.WithForexFillService(forexSvc)

	baseID := uint64(88)
	uid2 := uint64(42)
	order := &model.Order{
		ID:                  1,
		OwnerType:           model.OwnerClient,
		OwnerID:             &uid2,
		ListingID:           1,
		SecurityType:        "forex",
		Ticker:              "EUR/USD",
		Direction:           "buy",
		Quantity:            100,
		ContractSize:        1000,
		AccountID:           1, // quote account in the test (ACCT-001 is USD here)
		BaseAccountID:       &baseID,
		ReservationCurrency: "USD",
		SagaID:              "route-test-saga",
	}
	txn := &model.OrderTransaction{
		ID:           900,
		OrderID:      1,
		Quantity:     100,
		PricePerUnit: decimal.NewFromFloat(1.05),
		TotalPrice:   decimal.NewFromFloat(1.05).Mul(decimal.NewFromInt(100)).Mul(decimal.NewFromInt(1000)),
	}
	_ = mocks.txRepo.Create(txn)

	if err := svc.ProcessBuyFill(order, txn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Side effects we'd see from the forex saga (not the stock saga):
	// 1) exactly one PartialSettleReservation (quote debit).
	// 2) exactly one non-state CreditAccount (base credit).
	// 3) NO holding created (forex has no holding row).
	// 4) NO exchange-service Convert call (forex uses listing price, not FX).
	if len(mocks.fillClient.partialSettleCalls) != 1 {
		t.Errorf("expected 1 settle call from forex saga, got %d", len(mocks.fillClient.partialSettleCalls))
	}
	if len(mocks.fillClient.creditAccountCalls) != 1 {
		t.Errorf("expected 1 base credit from forex saga, got %d", len(mocks.fillClient.creditAccountCalls))
	}
	if len(mocks.exchangeClient.convertCalls) != 0 {
		t.Errorf("forex must not call exchange-service Convert, got %d calls", len(mocks.exchangeClient.convertCalls))
	}
	if len(mocks.holdingRepo.holdings) != 0 {
		t.Errorf("forex must not create a holding, got %d holdings", len(mocks.holdingRepo.holdings))
	}

	// The saga should carry a settle_reservation_quote step (forex-specific
	// step name), NOT settle_reservation (stock saga step name).
	var sawForexStep, sawStockStep bool
	for _, r := range mocks.sagaRepo.rows {
		if r.StepName == "settle_reservation_quote" {
			sawForexStep = true
		}
		if r.StepName == "settle_reservation" {
			sawStockStep = true
		}
	}
	if !sawForexStep {
		t.Error("expected settle_reservation_quote saga row (forex path)")
	}
	if sawStockStep {
		t.Error("stock-saga settle_reservation should NOT appear for forex")
	}
}
