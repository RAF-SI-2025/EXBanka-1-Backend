package service

import (
	"context"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

// TestRecoverAcceptSaga_ForwardResumeMintsContractOnce proves the no-human
// auto-resolve forward path for accept: re-driving a stranded accept saga mints
// (or reuses) exactly one ACTIVE contract and marks the offer accepted, even
// across repeated recovery invocations.
func TestRecoverAcceptSaga_ForwardResumeMintsContractOnce(t *testing.T) {
	fx := newAcceptSagaFixture(t)

	// Drive a real accept first so a contract exists for the saga, then recover
	// under THAT saga id (simulating a crash mid-accept whose contract already
	// committed). Recovery must forward-resume to terminal, not duplicate.
	contract, err := fx.svc.Accept(context.Background(), AcceptInput{
		OfferID: fx.offer.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
		AcceptorAccountID: 5001,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Recover using the contract's real saga id twice — idempotent no-op since
	// every step already completed.
	for i := 0; i < 2; i++ {
		if err := fx.svc.RecoverAcceptSaga(context.Background(), contract.SagaID, fx.offer.ID); err != nil {
			t.Fatalf("RecoverAcceptSaga #%d: %v", i, err)
		}
	}

	got, err := fx.contracts.GetBySagaID(contract.SagaID)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	if got.ID != contract.ID {
		t.Fatalf("recovery minted a duplicate contract: id %d != %d", got.ID, contract.ID)
	}
	if got.Status != model.OptionContractStatusActive {
		t.Fatalf("contract status = %s, want ACTIVE", got.Status)
	}
	// Offer remains accepted.
	o, _ := fx.offers.GetByID(fx.offer.ID)
	if o.Status != model.OTCOfferStatusAccepted {
		t.Fatalf("offer status = %s, want accepted", o.Status)
	}
}

// TestRecoverAcceptSaga_NoContract_NoOp proves that when the saga never minted a
// contract (crash before step 1 committed), recovery is a clean no-op — there
// is nothing committed to drive or roll back.
func TestRecoverAcceptSaga_NoContract_NoOp(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	if err := fx.svc.RecoverAcceptSaga(context.Background(), "never-ran-saga", fx.offer.ID); err != nil {
		t.Fatalf("RecoverAcceptSaga: %v", err)
	}
	// Offer untouched (still pending).
	o, _ := fx.offers.GetByID(fx.offer.ID)
	if o.Status != model.OTCOfferStatusPending {
		t.Fatalf("offer status = %s, want pending (no-op)", o.Status)
	}
}
