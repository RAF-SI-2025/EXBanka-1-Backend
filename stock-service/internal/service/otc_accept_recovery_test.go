package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
)

// mintActiveContractWithSaga mints an ACTIVE OptionContract bound to a KNOWN
// saga_id (so RecoverAcceptNegotiationSaga can load it via GetBySagaID) and
// reserves the seller's underlying holding — the residue an accept saga that
// crashed AFTER step 1 (reserve_and_contract) would have left behind. Premium
// and accounts are all RSD (no FX) so the assertions are exact.
func (fx *acceptSagaFixture) mintActiveContractWithSaga(t *testing.T, sagaID string) *model.OptionContract {
	t.Helper()
	buyerUID := uint64(fx.buyerID)
	sellerUID := uint64(fx.sellerID)
	offerID := fx.offer.ID
	c := &model.OptionContract{
		OfferID:        &offerID,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		StockID: fx.stockID, Ticker: fx.offer.Ticker,
		Quantity: fx.offer.Quantity, StrikePrice: decimal.NewFromInt(5000),
		PremiumPaid: decimal.NewFromInt(50000), PremiumCurrency: "RSD", StrikeCurrency: "RSD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, 7), Status: model.OptionContractStatusActive,
		SagaID: sagaID, PremiumPaidAt: time.Now().UTC(),
		BuyerAccountID: 5001, SellerAccountID: 6001,
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("mint contract: %v", err)
	}
	if _, err := fx.holdingResSvc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &sellerUID, "stock", fx.stockID, c.ID, c.Quantity.IntPart()); err != nil {
		t.Fatalf("reserve seller holding: %v", err)
	}
	return c
}

// recordCompletedForward seeds a COMPLETED forward saga_logs row so the saga
// executor's restart-resume skips that step on Execute (and so Compensate sees
// it as a step to undo).
func recordCompletedForward(t *testing.T, saga *fakeSagaRepo, sagaID, stepName string) {
	t.Helper()
	if err := saga.RecordStep(&model.SagaLog{
		SagaID:         sagaID,
		StepName:       stepName,
		Status:         model.SagaStatusCompleted,
		IsCompensation: false,
	}); err != nil {
		t.Fatalf("seed completed step %s: %v", stepName, err)
	}
}

// TestRecoverAcceptNegotiationSaga_ForwardResumeToCompletion proves the no-human
// auto-resolve forward path: a crash AFTER reserve_premium (steps 1+2 recorded
// completed, 3+4 not) is forward-resumed so the premium is settled off the buyer
// and credited to the seller — exactly once, even when recovery is invoked
// repeatedly (once per stuck row / across ticks).
func TestRecoverAcceptNegotiationSaga_ForwardResumeToCompletion(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	const sagaID = "accept-saga-fwd-1"
	contract := fx.mintActiveContractWithSaga(t, sagaID)

	// Simulate the crash point: steps 1 (reserve_and_contract) and 2
	// (reserve_premium) already committed; the settle + seller credit did not.
	recordCompletedForward(t, fx.saga, sagaID, string(saga.StepReserveAndContract))
	recordCompletedForward(t, fx.saga, sagaID, string(saga.StepReservePremium))

	// Drive recovery twice — idempotent.
	for i := 0; i < 2; i++ {
		if err := fx.svc.RecoverAcceptNegotiationSaga(context.Background(), sagaID); err != nil {
			t.Fatalf("RecoverAcceptNegotiationSaga #%d: %v", i, err)
		}
	}

	// Step 3 settled the buyer's premium exactly once (account-service enforces a
	// global UNIQUE(order_transaction_id); a replay must not re-settle).
	if got := fx.accounts.settleCalls; got != 1 {
		t.Fatalf("settleCalls = %d, want 1 (forward-resume must settle the premium exactly once)", got)
	}

	// Step 4 credited the seller the premium exactly once.
	sellerCredits := 0
	for _, c := range fx.accounts.creditCalls {
		if c.AccountNumber == "SELLER-RSD" {
			sellerCredits++
		}
	}
	if sellerCredits != 1 {
		t.Fatalf("seller credit count = %d, want 1 (replay must not double-credit)", sellerCredits)
	}
	if got := fx.accounts.sumCredited("SELLER-RSD"); !got.Equal(contract.PremiumPaid) {
		t.Fatalf("seller credited %s, want %s", got, contract.PremiumPaid)
	}

	// The contract remains ACTIVE (formation completed, not deleted).
	reload, err := fx.contracts.GetByID(contract.ID)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	if reload.Status != model.OptionContractStatusActive {
		t.Fatalf("contract status = %s, want ACTIVE", reload.Status)
	}
}

// TestRecoverAcceptNegotiationSaga_CompensateReleasesAndRefunds proves mode
// selection + rollback: when the persisted saga already has compensation rows
// (it was aborting), the recoverer takes the Compensate path and undoes the
// committed steps in reverse — refunding the buyer's settled premium, releasing
// the buyer's reservation, releasing the seller's share reservation, and
// deleting the half-formed contract.
func TestRecoverAcceptNegotiationSaga_CompensateReleasesAndRefunds(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	const sagaID = "accept-saga-comp-1"
	contract := fx.mintActiveContractWithSaga(t, sagaID)

	// Steps 1, 2 and 3 committed before the abort began; the saga had started
	// rolling back when the process died.
	recordCompletedForward(t, fx.saga, sagaID, string(saga.StepReserveAndContract))
	recordCompletedForward(t, fx.saga, sagaID, string(saga.StepReservePremium))
	recordCompletedForward(t, fx.saga, sagaID, string(saga.StepSettlePremiumBuyer))
	fx.saga.hasComp = true // a compensation row exists → rollback direction

	releaseBefore := fx.accounts.releaseCalls
	if err := fx.svc.RecoverAcceptNegotiationSaga(context.Background(), sagaID); err != nil {
		t.Fatalf("RecoverAcceptNegotiationSaga: %v", err)
	}

	// Step 2 backward released the buyer's premium reservation.
	if fx.accounts.releaseCalls <= releaseBefore {
		t.Fatalf("releaseCalls = %d, want > %d (reservation must be released on rollback)", fx.accounts.releaseCalls, releaseBefore)
	}

	// Step 3 backward refunded the buyer the settled premium.
	buyerCredits := 0
	for _, c := range fx.accounts.creditCalls {
		if c.AccountNumber == "BUYER-RSD" {
			buyerCredits++
		}
	}
	if buyerCredits == 0 {
		t.Fatalf("buyer was not refunded on rollback (want a BUYER-RSD credit)")
	}
	if got := fx.accounts.sumCredited("BUYER-RSD"); !got.Equal(contract.PremiumPaid) {
		t.Fatalf("buyer refunded %s, want %s", got, contract.PremiumPaid)
	}

	// Step 1 backward deleted the half-formed contract.
	if _, err := fx.contracts.GetByID(contract.ID); err == nil {
		t.Fatalf("contract must be deleted on rollback, but it still exists")
	}

	// Step 1 backward released the seller's share reservation (back to 0).
	sellerUID := uint64(fx.sellerID)
	h, err := fx.holdings.GetByOwnerAndSecurity(model.OwnerClient, &sellerUID, "stock", fx.stockID)
	if err != nil {
		t.Fatalf("seller holding lookup: %v", err)
	}
	if h.ReservedQuantity != 0 {
		t.Fatalf("seller holding reserved = %d, want 0 (reservation must be released on rollback)", h.ReservedQuantity)
	}
}
