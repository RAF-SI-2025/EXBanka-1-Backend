package service

import (
	"context"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

// TestRecoverExerciseSaga_ForwardResumeToExercised proves the no-human
// auto-resolve forward path: re-driving a stranded exercise saga drives the
// contract to EXERCISED and credits the buyer's holding exactly once — even
// when called repeatedly (crash recovery may invoke it once per stuck row and
// across ticks).
func TestRecoverExerciseSaga_ForwardResumeToExercised(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	// Accept first to mint a real ACTIVE contract with the seller's holding
	// reserved (this is the precondition the exercise saga operates on).
	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Drive recovery twice — idempotent.
	for i := 0; i < 2; i++ {
		if err := fx.svc.RecoverExerciseSaga(context.Background(), "exercise-saga-1", contract.ID); err != nil {
			t.Fatalf("RecoverExerciseSaga #%d: %v", i, err)
		}
	}

	got, err := fx.contracts.GetByID(contract.ID)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	if got.Status != model.OptionContractStatusExercised {
		t.Fatalf("contract status = %s, want EXERCISED", got.Status)
	}

	// Buyer credited exactly once despite two recovery runs (marker guard).
	buyerUID := uint64(fx.buyerID)
	h, err := fx.holdings.GetByOwnerAndSecurity(model.OwnerClient, &buyerUID, "stock", fx.stockID)
	if err != nil {
		t.Fatalf("buyer holding lookup: %v", err)
	}
	wantQty := contract.Quantity.IntPart()
	if h.Quantity != wantQty {
		t.Fatalf("buyer holding quantity = %d, want %d (replay must not double-credit)", h.Quantity, wantQty)
	}
}

// TestRecoverExerciseSaga_RollbackModeDoesNotExercise proves mode selection:
// when the persisted saga already has compensation rows (it was aborting), the
// recoverer takes the rollback path and never drives the contract to EXERCISED.
func TestRecoverExerciseSaga_RollbackModeDoesNotExercise(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	fx.saga.hasComp = true // saga had begun rolling back

	if err := fx.svc.RecoverExerciseSaga(context.Background(), "exercise-saga-2", contract.ID); err != nil {
		t.Fatalf("RecoverExerciseSaga: %v", err)
	}

	got, err := fx.contracts.GetByID(contract.ID)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	if got.Status != model.OptionContractStatusActive {
		t.Fatalf("rollback mode must leave contract ACTIVE, got %s", got.Status)
	}
	buyerUID := uint64(fx.buyerID)
	if _, err := fx.holdings.GetByOwnerAndSecurity(model.OwnerClient, &buyerUID, "stock", fx.stockID); err == nil {
		t.Fatalf("rollback mode must not credit the buyer's holding")
	}
}
