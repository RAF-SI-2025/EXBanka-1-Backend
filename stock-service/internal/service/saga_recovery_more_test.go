package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// fakeAcceptRecoverer records RecoverAcceptNegotiationSaga calls.
type fakeAcceptRecoverer struct {
	calls []string
	err   error
}

func (f *fakeAcceptRecoverer) RecoverAcceptNegotiationSaga(_ context.Context, sagaID string) error {
	f.calls = append(f.calls, sagaID)
	return f.err
}

func TestSagaRecovery_AcceptStep_AutoResolvesViaRecoverer(t *testing.T) {
	step := settleStep(1, 7777, 9001, decimal.NewFromInt(1), "reserve_premium")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeAcceptRecoverer{}

	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).
		WithAcceptRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(recvr.calls) != 1 || recvr.calls[0] != "saga-1" {
		t.Fatalf("expected RecoverAcceptNegotiationSaga(saga-1), got %+v", recvr.calls)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Fatalf("expected one completed update, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_AcceptStep_Compensation_MarksCompensated(t *testing.T) {
	step := settleStep(1, 7777, 9001, decimal.NewFromInt(1), "credit_premium_seller")
	step.IsCompensation = true
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeAcceptRecoverer{}

	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).
		WithAcceptRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompensated {
		t.Fatalf("expected compensated update, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_AcceptStep_RecovererError_IncrementsRetry(t *testing.T) {
	step := settleStep(1, 7777, 9001, decimal.NewFromInt(1), "settle_premium_buyer")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeAcceptRecoverer{err: errors.New("boom")}

	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).
		WithAcceptRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// On recoverer error the row is NOT marked completed; retry count is bumped.
	if len(repo.updateCalls) != 0 {
		t.Errorf("expected no status update on recoverer error, got %+v", repo.updateCalls)
	}
	if len(repo.incrementCalls) != 1 {
		t.Errorf("expected one retry increment, got %d", len(repo.incrementCalls))
	}
}

func TestSagaRecovery_AcceptStep_MissingSagaID_LogAndLeave(t *testing.T) {
	step := model.SagaLog{
		ID: 1, SagaID: "", OrderID: 7777, StepName: "reserve_and_contract",
		Status: model.SagaStatusPending, Version: 1,
	}
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeAcceptRecoverer{}

	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).
		WithAcceptRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(recvr.calls) != 0 {
		t.Errorf("recoverer should not be called without a saga_id")
	}
	if len(repo.updateCalls) != 0 {
		t.Errorf("missing saga_id → log-and-leave, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_FundStep_RecovererError_IncrementsRetry(t *testing.T) {
	step := settleStep(1, 3131, 9001, decimal.NewFromInt(1), "debit_fund")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeFundRecoverer{err: errors.New("nope")}

	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).
		WithFundRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.incrementCalls) != 1 {
		t.Errorf("expected one retry increment, got %d", len(repo.incrementCalls))
	}
}

func TestSagaRecovery_CrossbankAndTxnSteps_LogAndLeave(t *testing.T) {
	// One step name from each "should not be here → log and leave" switch arm.
	for _, stepName := range []string{
		"reserve_buyer_funds", // crossbank
		"mark_expired",        // crossbank
		"debit_sender",        // transaction-service
		"credit_recipient",    // transaction-service
	} {
		t.Run(stepName, func(t *testing.T) {
			step := settleStep(1, 100, 9001, decimal.NewFromInt(1), stepName)
			repo := newFakeRecoveryRepo(step)
			client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
			rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry())
			if err := rec.Reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if len(repo.updateCalls) != 0 {
				t.Errorf("step %s should be log-and-leave, got %+v", stepName, repo.updateCalls)
			}
		})
	}
}

func TestSagaRecovery_FillStep_NoDepsWired_LogAndLeave(t *testing.T) {
	step := settleStep(1, 555, 8001, decimal.NewFromInt(1), "record_transaction")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	// No WithFillRecoverer → fill steps fall back to log-and-leave.
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry())
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 0 {
		t.Fatalf("fill step without deps should be log-and-leave, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_FundStep_NoRecoverer_LogAndLeave(t *testing.T) {
	step := settleStep(1, 3131, 9001, decimal.NewFromInt(1), "debit_fund")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()) // no fund recoverer
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 0 {
		t.Fatalf("fund step without recoverer should be log-and-leave, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_FundStep_MissingSagaID_LogAndLeave(t *testing.T) {
	step := model.SagaLog{ID: 1, SagaID: "", OrderID: 3131, StepName: "credit_fund", Status: model.SagaStatusPending, Version: 1}
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeFundRecoverer{}
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).WithFundRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(recvr.calls) != 0 || len(repo.updateCalls) != 0 {
		t.Fatalf("missing saga_id → log-and-leave; recvr=%d updates=%+v", len(recvr.calls), repo.updateCalls)
	}
}

func TestSagaRecovery_ExerciseStep_Compensation_MarksCompensated(t *testing.T) {
	step := settleStep(1, 4242, 9001, decimal.NewFromInt(1), "reserve_strike")
	step.IsCompensation = true
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakeExerciseRecoverer{}
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).WithExerciseRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompensated {
		t.Fatalf("expected compensated update, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_PlacementStep_Compensation_MarksCompensated(t *testing.T) {
	step := settleStep(1, 5050, 9001, decimal.NewFromInt(1), "reserve_funds")
	step.IsCompensation = true
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	recvr := &fakePlacementRecoverer{}
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry()).WithPlacementRecoverer(recvr)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompensated {
		t.Fatalf("expected compensated update, got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_UpdateHolding_MarksCompletedDirect(t *testing.T) {
	step := settleStep(1, 100, 9001, decimal.NewFromInt(1), "update_holding")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry())
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Fatalf("expected completed (idempotent upsert), got %+v", repo.updateCalls)
	}
}

func TestSagaRecovery_DecrementHolding_MarksCompleted(t *testing.T) {
	step := settleStep(1, 100, 9001, decimal.NewFromInt(1), "decrement_holding")
	repo := newFakeRecoveryRepo(step)
	client := newFakeRecoveryFillClient(&fakeRecoveryAccountStub{})
	rec := NewSagaRecovery(repo, client, nil, "", nil, nilRegistry())
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(repo.updateCalls) != 1 || repo.updateCalls[0].NewStatus != model.SagaStatusCompleted {
		t.Fatalf("expected completed (idempotent settlement), got %+v", repo.updateCalls)
	}
}
