package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeRecoveryRepo satisfies SagaRecoveryLogRepo. It records UpdateStatus and
// IncrementRetryCount invocations so tests can assert the reconciler's
// decisions against it.
type fakeRecoveryRepo struct {
	mu                sync.Mutex
	stuck             []model.SagaLog
	updateCalls       []updateStatusCall
	incrementCalls    []uint64
	forceListErr      error
	forceUpdateErr    error
	forceIncrementErr error
}

type updateStatusCall struct {
	ID        uint64
	Version   int64
	NewStatus string
	ErrMsg    string
}

func newFakeRecoveryRepo(stuck ...model.SagaLog) *fakeRecoveryRepo {
	return &fakeRecoveryRepo{stuck: stuck}
}

func (r *fakeRecoveryRepo) ListStuckSagas(_ time.Duration) ([]model.SagaLog, error) {
	if r.forceListErr != nil {
		return nil, r.forceListErr
	}
	out := make([]model.SagaLog, len(r.stuck))
	copy(out, r.stuck)
	return out, nil
}

func (r *fakeRecoveryRepo) UpdateStatus(id uint64, version int64, newStatus, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.forceUpdateErr != nil {
		return r.forceUpdateErr
	}
	r.updateCalls = append(r.updateCalls, updateStatusCall{
		ID: id, Version: version, NewStatus: newStatus, ErrMsg: errMsg,
	})
	return nil
}

func (r *fakeRecoveryRepo) IncrementRetryCount(id uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.forceIncrementErr != nil {
		return r.forceIncrementErr
	}
	r.incrementCalls = append(r.incrementCalls, id)
	return nil
}

// fakeRecoveryFillClient is a narrow fake for FillAccountClient that records
// PartialSettleReservation calls and answers GetReservation via its embedded
// stub. Only the methods the reconciler uses are populated; the rest return
// zero values / nil.
type fakeRecoveryFillClient struct {
	stub *fakeRecoveryAccountStub

	partialSettleErr   error
	partialSettleCalls []partialSettleRecoveryCall

	creditErr   error
	creditCalls []creditDebitRecoveryCall
	debitErr    error
	debitCalls  []creditDebitRecoveryCall
}

type partialSettleRecoveryCall struct {
	OrderID uint64
	TxnID   uint64
	Amount  decimal.Decimal
	Memo    string
}

type creditDebitRecoveryCall struct {
	AccountNumber  string
	Amount         decimal.Decimal
	Memo           string
	IdempotencyKey string
}

func newFakeRecoveryFillClient(stub *fakeRecoveryAccountStub) *fakeRecoveryFillClient {
	return &fakeRecoveryFillClient{stub: stub}
}

func (c *fakeRecoveryFillClient) PartialSettleReservation(_ context.Context, orderID, txnID uint64, amount decimal.Decimal, memo string) (*accountpb.PartialSettleReservationResponse, error) {
	if c.partialSettleErr != nil {
		return nil, c.partialSettleErr
	}
	c.partialSettleCalls = append(c.partialSettleCalls, partialSettleRecoveryCall{
		OrderID: orderID, TxnID: txnID, Amount: amount, Memo: memo,
	})
	return &accountpb.PartialSettleReservationResponse{}, nil
}

func (c *fakeRecoveryFillClient) CreditAccount(_ context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error) {
	if c.creditErr != nil {
		return nil, c.creditErr
	}
	c.creditCalls = append(c.creditCalls, creditDebitRecoveryCall{
		AccountNumber: accountNumber, Amount: amount, Memo: memo, IdempotencyKey: idempotencyKey,
	})
	return &accountpb.AccountResponse{}, nil
}

func (c *fakeRecoveryFillClient) DebitAccount(_ context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error) {
	if c.debitErr != nil {
		return nil, c.debitErr
	}
	c.debitCalls = append(c.debitCalls, creditDebitRecoveryCall{
		AccountNumber: accountNumber, Amount: amount, Memo: memo, IdempotencyKey: idempotencyKey,
	})
	return &accountpb.AccountResponse{}, nil
}

func (c *fakeRecoveryFillClient) Stub() accountpb.AccountServiceClient { return c.stub }

// fakeRecoveryAccountStub implements accountpb.AccountServiceClient with a
// canned GetReservation response. Other methods return zero values except
// GetAccount, which returns a canned response so the credit/debit recovery
// path can resolve an account ID to an account number.
type fakeRecoveryAccountStub struct {
	getReservationResp      *accountpb.GetReservationResponse
	getReservationErr       error
	getReservationCallCount int

	getAccountResp *accountpb.AccountResponse
	getAccountErr  error
}

func (s *fakeRecoveryAccountStub) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	s.getReservationCallCount++
	if s.getReservationErr != nil {
		return nil, s.getReservationErr
	}
	return s.getReservationResp, nil
}

// Unused AccountServiceClient methods — stubbed to satisfy the interface.
func (s *fakeRecoveryAccountStub) CreateAccount(context.Context, *accountpb.CreateAccountRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) GetAccount(context.Context, *accountpb.GetAccountRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getAccountErr != nil {
		return nil, s.getAccountErr
	}
	if s.getAccountResp != nil {
		return s.getAccountResp, nil
	}
	return &accountpb.AccountResponse{}, nil
}
func (s *fakeRecoveryAccountStub) GetAccountByNumber(context.Context, *accountpb.GetAccountByNumberRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) ListAccountsByClient(context.Context, *accountpb.ListAccountsByClientRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) ListAllAccounts(context.Context, *accountpb.ListAllAccountsRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) UpdateAccountName(context.Context, *accountpb.UpdateAccountNameRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) UpdateAccountLimits(context.Context, *accountpb.UpdateAccountLimitsRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) UpdateAccountStatus(context.Context, *accountpb.UpdateAccountStatusRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) UpdateBalance(context.Context, *accountpb.UpdateBalanceRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) CreateCompany(context.Context, *accountpb.CreateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) GetCompany(context.Context, *accountpb.GetCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) UpdateCompany(context.Context, *accountpb.UpdateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) ListCurrencies(context.Context, *accountpb.ListCurrenciesRequest, ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) GetCurrency(context.Context, *accountpb.GetCurrencyRequest, ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) GetLedgerEntries(context.Context, *accountpb.GetLedgerEntriesRequest, ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) ReserveFunds(context.Context, *accountpb.ReserveFundsRequest, ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) ReleaseReservation(context.Context, *accountpb.ReleaseReservationRequest, ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (s *fakeRecoveryAccountStub) PartialSettleReservation(context.Context, *accountpb.PartialSettleReservationRequest, ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func settleStep(id, orderID, txnID uint64, amount decimal.Decimal, stepName string) model.SagaLog {
	txn := txnID
	amt := amount
	return model.SagaLog{
		ID:                 id,
		SagaID:             "saga-1",
		OrderID:            orderID,
		OrderTransactionID: &txn,
		StepNumber:         3,
		StepName:           stepName,
		Status:             model.SagaStatusPending,
		Amount:             &amt,
		CurrencyCode:       "RSD",
		Version:            1,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// If account-service already has the txnID in SettledTransactionIds the
// reconciler should mark the saga row completed without issuing another
// PartialSettleReservation call.
func TestSagaRecovery_SettlementAlreadyCommitted_MarksCompleted(t *testing.T) {
	step := settleStep(1, 100, 200, decimal.NewFromFloat(1234.56), "settle_reservation")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getReservationResp: &accountpb.GetReservationResponse{
			Exists:                true,
			Status:                "active",
			SettledTransactionIds: []uint64{200}, // already contains our txnID
		},
	}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(repo.updateCalls) != 1 {
		t.Fatalf("expected 1 UpdateStatus call, got %d", len(repo.updateCalls))
	}
	if repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Errorf("status: got %s want completed", repo.updateCalls[0].NewStatus)
	}
	if len(client.partialSettleCalls) != 0 {
		t.Errorf("expected no PartialSettleReservation retries, got %d", len(client.partialSettleCalls))
	}
}

// When the txnID is not yet on the reservation, the reconciler should retry
// PartialSettleReservation and mark the row completed on success.
func TestSagaRecovery_SettlementMissing_RetriesAndCompletes(t *testing.T) {
	amount := decimal.NewFromFloat(555.25)
	step := settleStep(2, 101, 201, amount, "settle_reservation")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getReservationResp: &accountpb.GetReservationResponse{
			Exists:                true,
			Status:                "active",
			SettledTransactionIds: []uint64{}, // empty → need to retry
		},
	}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.partialSettleCalls) != 1 {
		t.Fatalf("expected 1 PartialSettleReservation retry, got %d", len(client.partialSettleCalls))
	}
	got := client.partialSettleCalls[0]
	if got.OrderID != 101 || got.TxnID != 201 || !got.Amount.Equal(amount) {
		t.Errorf("retry args: %+v", got)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Errorf("expected UpdateStatus(..., completed), got %+v", repo.updateCalls)
	}
}

// forex quote settlement uses a distinct memo prefix — check it survives
// through the recovery retry.
func TestSagaRecovery_QuoteSettlementMissing_RetriesWithForexMemo(t *testing.T) {
	step := settleStep(3, 102, 202, decimal.NewFromFloat(99.99), "settle_reservation_quote")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getReservationResp: &accountpb.GetReservationResponse{
			Exists:                true,
			SettledTransactionIds: []uint64{},
		},
	}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.partialSettleCalls) != 1 {
		t.Fatalf("expected 1 PartialSettleReservation retry, got %d", len(client.partialSettleCalls))
	}
	memo := client.partialSettleCalls[0].Memo
	if memo == "" || memo == "recovery settlement" {
		t.Errorf("expected forex-specific memo, got %q", memo)
	}
}

// Steps that have already exceeded the retry ceiling are left untouched with
// a loud error log and no further action.
func TestSagaRecovery_MaxRetriesExceeded_LogsAndSkips(t *testing.T) {
	step := settleStep(4, 103, 203, decimal.NewFromFloat(10), "settle_reservation")
	step.RetryCount = maxSagaRecoveryRetries
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if stub.getReservationCallCount != 0 {
		t.Errorf("expected no GetReservation calls for a ceiling-reached step, got %d", stub.getReservationCallCount)
	}
	if len(client.partialSettleCalls) != 0 {
		t.Errorf("expected no retry calls, got %d", len(client.partialSettleCalls))
	}
	if len(repo.updateCalls) != 0 {
		t.Errorf("expected no UpdateStatus calls, got %d", len(repo.updateCalls))
	}
	if len(repo.incrementCalls) != 0 {
		t.Errorf("expected no IncrementRetryCount calls, got %d", len(repo.incrementCalls))
	}
}

// When no orderRepo is wired, credit/debit steps fall back to the degraded
// "log and leave alone" behaviour: no account-service calls, no status change.
func TestSagaRecovery_CreditStep_WithoutOrderRepo_LeftAlone(t *testing.T) {
	step := settleStep(5, 104, 204, decimal.NewFromFloat(1), "credit_proceeds")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if stub.getReservationCallCount != 0 {
		t.Errorf("expected no GetReservation calls, got %d", stub.getReservationCallCount)
	}
	if len(client.partialSettleCalls) != 0 {
		t.Errorf("expected no PartialSettleReservation calls, got %d", len(client.partialSettleCalls))
	}
	if len(client.creditCalls) != 0 {
		t.Errorf("expected no CreditAccount calls without orderRepo wiring, got %d", len(client.creditCalls))
	}
	if len(repo.updateCalls) != 0 {
		t.Errorf("expected no UpdateStatus calls, got %d", len(repo.updateCalls))
	}
}

// Placement-saga steps must NOT be auto-retried — replaying them would
// double-place a user order.
func TestSagaRecovery_PlacementStep_NotAutoRetried(t *testing.T) {
	for _, name := range []string{
		"persist_order_pending", "validate_listing", "reserve_funds", "reserve_holding",
	} {
		t.Run(name, func(t *testing.T) {
			step := settleStep(6, 105, 205, decimal.NewFromFloat(1), name)
			repo := newFakeRecoveryRepo(step)

			stub := &fakeRecoveryAccountStub{}
			client := newFakeRecoveryFillClient(stub)

			rec := NewSagaRecovery(repo, client, nil, "")
			if err := rec.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			if stub.getReservationCallCount != 0 {
				t.Errorf("expected no GetReservation calls, got %d", stub.getReservationCallCount)
			}
			if len(client.partialSettleCalls) != 0 {
				t.Errorf("expected no PartialSettleReservation calls, got %d", len(client.partialSettleCalls))
			}
			if len(repo.updateCalls) != 0 {
				t.Errorf("expected no UpdateStatus calls, got %d", len(repo.updateCalls))
			}
		})
	}
}

// update_holding is auto-completed because the holding upsert is idempotent.
func TestSagaRecovery_UpdateHolding_MarksCompleted(t *testing.T) {
	step := settleStep(7, 106, 206, decimal.NewFromFloat(1), "update_holding")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Errorf("expected 1 completed UpdateStatus, got %+v", repo.updateCalls)
	}
}

// When the retry itself fails, IncrementRetryCount is called so repeated
// failures can eventually hit the ceiling and stop being retried.
func TestSagaRecovery_RetryFailure_IncrementsRetryCount(t *testing.T) {
	step := settleStep(8, 107, 207, decimal.NewFromFloat(1), "settle_reservation")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getReservationResp: &accountpb.GetReservationResponse{
			Exists:                true,
			SettledTransactionIds: []uint64{},
		},
	}
	client := newFakeRecoveryFillClient(stub)
	client.partialSettleErr = errors.New("boom")

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(repo.incrementCalls) != 1 || repo.incrementCalls[0] != step.ID {
		t.Errorf("expected IncrementRetryCount(%d), got %v", step.ID, repo.incrementCalls)
	}
	if len(repo.updateCalls) != 0 {
		t.Errorf("expected no UpdateStatus on failed retry, got %+v", repo.updateCalls)
	}
}

// An unknown step name is logged and left alone (no retry, no status change).
func TestSagaRecovery_UnknownStep_LeftAlone(t *testing.T) {
	step := settleStep(9, 108, 208, decimal.NewFromFloat(1), "this_is_not_a_real_step")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{}
	client := newFakeRecoveryFillClient(stub)

	rec := NewSagaRecovery(repo, client, nil, "")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(repo.updateCalls) != 0 || len(client.partialSettleCalls) != 0 {
		t.Errorf("expected no actions on unknown step, got updates=%+v settles=%+v",
			repo.updateCalls, client.partialSettleCalls)
	}
}

// Run wires Reconcile onto a ticker and honors ctx cancellation.
func TestSagaRecovery_Run_StopsOnContextCancel(t *testing.T) {
	repo := newFakeRecoveryRepo() // empty — nothing to reconcile
	stub := &fakeRecoveryAccountStub{}
	client := newFakeRecoveryFillClient(stub)

	ctx, cancel := context.WithCancel(context.Background())
	rec := NewSagaRecovery(repo, client, nil, "")
	rec.Run(ctx, 10*time.Millisecond)

	// Give the initial Reconcile a moment to run, then cancel. We only
	// assert that cancelling doesn't deadlock or panic.
	time.Sleep(25 * time.Millisecond)
	cancel()
	time.Sleep(15 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Auto-retry for credit/debit steps (the idempotency-key unlock)
// ---------------------------------------------------------------------------

// fakeRecoveryOrderRepo satisfies RecoveryOrderRepo with a canned order lookup.
type fakeRecoveryOrderRepo struct {
	orders map[uint64]*model.Order
	err    error
}

func (r *fakeRecoveryOrderRepo) GetByID(id uint64) (*model.Order, error) {
	if r.err != nil {
		return nil, r.err
	}
	if o, ok := r.orders[id]; ok {
		return o, nil
	}
	return nil, errors.New("order not found")
}

// credit_proceeds auto-retries with key "sell-credit-{txnID}" on the user's
// main account and marks the saga row completed on success.
func TestSagaRecovery_CreditProceeds_AutoRetriesWithKey(t *testing.T) {
	const orderID, txnID uint64 = 300, 400
	amount := decimal.NewFromFloat(789.25)
	step := settleStep(1, orderID, txnID, amount, "credit_proceeds")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getAccountResp: &accountpb.AccountResponse{AccountNumber: "USER-ACCT-1"},
	}
	client := newFakeRecoveryFillClient(stub)
	orderRepo := &fakeRecoveryOrderRepo{orders: map[uint64]*model.Order{
		orderID: {ID: orderID, AccountID: 11},
	}}

	rec := NewSagaRecovery(repo, client, orderRepo, "STATE-ACCT-001")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.creditCalls) != 1 {
		t.Fatalf("expected 1 CreditAccount retry, got %d", len(client.creditCalls))
	}
	got := client.creditCalls[0]
	if got.AccountNumber != "USER-ACCT-1" {
		t.Errorf("AccountNumber: got %q want USER-ACCT-1", got.AccountNumber)
	}
	if !got.Amount.Equal(amount) {
		t.Errorf("Amount: got %s want %s", got.Amount, amount)
	}
	wantKey := "sell-credit-400"
	if got.IdempotencyKey != wantKey {
		t.Errorf("IdempotencyKey: got %q want %q", got.IdempotencyKey, wantKey)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Errorf("expected UpdateStatus(completed), got %+v", repo.updateCalls)
	}
}

// credit_commission retries land on the state account with key "commission-{txnID}".
func TestSagaRecovery_CommissionCredit_AutoRetriesWithKey(t *testing.T) {
	const orderID, txnID uint64 = 301, 401
	step := settleStep(2, orderID, txnID, decimal.NewFromFloat(1.25), "credit_commission")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{}
	client := newFakeRecoveryFillClient(stub)
	orderRepo := &fakeRecoveryOrderRepo{orders: map[uint64]*model.Order{
		orderID: {ID: orderID, AccountID: 99},
	}}

	rec := NewSagaRecovery(repo, client, orderRepo, "STATE-ACCT-001")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.creditCalls) != 1 {
		t.Fatalf("expected 1 CreditAccount retry, got %d", len(client.creditCalls))
	}
	got := client.creditCalls[0]
	if got.AccountNumber != "STATE-ACCT-001" {
		t.Errorf("AccountNumber: got %q want STATE-ACCT-001", got.AccountNumber)
	}
	wantKey := "commission-401"
	if got.IdempotencyKey != wantKey {
		t.Errorf("IdempotencyKey: got %q want %q", got.IdempotencyKey, wantKey)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Errorf("expected UpdateStatus(completed), got %+v", repo.updateCalls)
	}
}

// compensate_settle_via_credit (buy-side compensation) retries with key
// "compensate-buy-{txnID}" on the user's main account.
func TestSagaRecovery_CompensationCredit_AutoRetriesWithKey(t *testing.T) {
	const orderID, txnID uint64 = 302, 402
	step := settleStep(3, orderID, txnID, decimal.NewFromFloat(500), "compensate_settle_via_credit")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getAccountResp: &accountpb.AccountResponse{AccountNumber: "USER-ACCT-2"},
	}
	client := newFakeRecoveryFillClient(stub)
	orderRepo := &fakeRecoveryOrderRepo{orders: map[uint64]*model.Order{
		orderID: {ID: orderID, AccountID: 22},
	}}

	rec := NewSagaRecovery(repo, client, orderRepo, "STATE-ACCT-001")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.creditCalls) != 1 {
		t.Fatalf("expected 1 CreditAccount retry, got %d", len(client.creditCalls))
	}
	if got := client.creditCalls[0].IdempotencyKey; got != "compensate-buy-402" {
		t.Errorf("IdempotencyKey: got %q want compensate-buy-402", got)
	}
}

// compensate_credit_via_debit (sell-side compensation) retries via DebitAccount
// with key "compensate-sell-{txnID}".
func TestSagaRecovery_SellCompensationDebit_AutoRetriesWithKey(t *testing.T) {
	const orderID, txnID uint64 = 303, 403
	step := settleStep(4, orderID, txnID, decimal.NewFromFloat(25), "compensate_credit_via_debit")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getAccountResp: &accountpb.AccountResponse{AccountNumber: "USER-ACCT-3"},
	}
	client := newFakeRecoveryFillClient(stub)
	orderRepo := &fakeRecoveryOrderRepo{orders: map[uint64]*model.Order{
		orderID: {ID: orderID, AccountID: 33},
	}}

	rec := NewSagaRecovery(repo, client, orderRepo, "STATE-ACCT-001")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(client.debitCalls) != 1 {
		t.Fatalf("expected 1 DebitAccount retry, got %d", len(client.debitCalls))
	}
	if got := client.debitCalls[0].IdempotencyKey; got != "compensate-sell-403" {
		t.Errorf("IdempotencyKey: got %q want compensate-sell-403", got)
	}
	if len(client.creditCalls) != 0 {
		t.Errorf("no CreditAccount calls expected for sell-side compensation, got %d", len(client.creditCalls))
	}
}

// When the retry RPC errors, the reconciler increments retry_count and leaves
// the row's status untouched — so repeated failures hit the ceiling instead of
// silently looping forever.
func TestSagaRecovery_CreditRetryFailure_IncrementsRetryCount(t *testing.T) {
	const orderID, txnID uint64 = 304, 404
	step := settleStep(5, orderID, txnID, decimal.NewFromFloat(1), "credit_proceeds")
	repo := newFakeRecoveryRepo(step)

	stub := &fakeRecoveryAccountStub{
		getAccountResp: &accountpb.AccountResponse{AccountNumber: "USER-ACCT-4"},
	}
	client := newFakeRecoveryFillClient(stub)
	client.creditErr = errors.New("boom")
	orderRepo := &fakeRecoveryOrderRepo{orders: map[uint64]*model.Order{
		orderID: {ID: orderID, AccountID: 44},
	}}

	rec := NewSagaRecovery(repo, client, orderRepo, "STATE-ACCT-001")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(repo.incrementCalls) != 1 {
		t.Errorf("expected 1 IncrementRetryCount call, got %d", len(repo.incrementCalls))
	}
	if len(repo.updateCalls) != 0 {
		t.Errorf("expected no UpdateStatus on failed retry, got %+v", repo.updateCalls)
	}
}
