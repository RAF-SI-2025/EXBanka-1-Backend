package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

// TestExerciseContract_FailsAtCapitalGain_Compensates makes the
// record_seller_strike_gain step fail so the saga executor walks every prior
// step's Backward closure (release strike reservation, credit-back the buyer,
// debit-back the seller, restore the consumed seller shares, remove the buyer's
// credited holding). This exercises buildExerciseSaga's compensation paths.
func TestExerciseContract_FailsAtCapitalGain_Compensates(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	cgRepo := newMockCapitalGainRepo()
	cgRepo.failNextCreate = errors.New("cg write failed")
	fx.svc = fx.svc.WithCapitalGain(cgRepo)

	contract := fx.mintActiveContract(t)

	// Seller's holding before exercise: 100 shares, 10 reserved for the contract.
	before, _ := fx.holdings.GetByID(holdingIDForSeller(t, fx))

	_, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: contract.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected exercise to fail at the capital-gain step")
	}

	// Compensation released the buyer's strike reservation.
	if fx.accounts.releaseCalls == 0 {
		t.Errorf("expected the strike reservation to be released on compensation")
	}

	// The consumed seller shares were restored: quantity back to its pre-exercise
	// value (the consume_seller_holding Backward = RestoreForOTCContract).
	after, _ := fx.holdings.GetByID(before.ID)
	if after.Quantity != before.Quantity {
		t.Errorf("seller quantity = %d, want restored to %d", after.Quantity, before.Quantity)
	}

	// The contract must remain ACTIVE (never flipped to exercised).
	got, _ := fx.contracts.GetByID(contract.ID)
	if got.Status != model.OptionContractStatusActive {
		t.Errorf("contract status = %s, want ACTIVE after failed exercise", got.Status)
	}
}

// holdingIDForSeller returns the seller's holding row id for the fixture's stock.
func holdingIDForSeller(t *testing.T, fx *acceptSagaFixture) uint64 {
	t.Helper()
	sellerUID := uint64(fx.sellerID)
	h, err := fx.holdings.GetByOwnerAndSecurity(model.OwnerClient, &sellerUID, "stock", fx.stockID)
	if err != nil {
		t.Fatalf("seller holding lookup: %v", err)
	}
	return h.ID
}
