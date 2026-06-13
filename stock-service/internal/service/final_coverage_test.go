package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestMintContract_OnBehalfOfFund covers MintContractFromAcceptedNegotiation's
// OnBehalfOfFundID tagging branch.
func TestMintContract_OnBehalfOfFund(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	buyerUID := uint64(fx.buyerID)
	neg := &model.OTCNegotiation{
		ParentOfferID:   fx.offer.ID,
		BidderOwnerType: model.OwnerClient,
		BidderOwnerID:   &buyerUID,
		BidderAccountID: 5001,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(5000),
		Premium:         decimal.NewFromInt(50000),
		SettlementDate:  time.Now().UTC().AddDate(0, 0, 7),
		Status:          model.OTCNegotiationStatusAccepted,
	}
	contract, err := fx.svc.MintContractFromAcceptedNegotiation(context.Background(), MintFromNegotiationInput{
		Parent: fx.offer, Negotiation: neg,
		AcceptorOwnerType: model.OwnerClient, AcceptorOwnerID: &buyerUID, AcceptorAccountID: 5001,
		ActorPrincipalType: "client", ActorPrincipalID: buyerUID,
		OnBehalfOfFundID: 7,
	})
	if err != nil {
		t.Fatalf("mint on behalf of fund: %v", err)
	}
	if contract.OnBehalfOfFundID == nil || *contract.OnBehalfOfFundID != 7 {
		t.Errorf("OnBehalfOfFundID = %v, want 7", contract.OnBehalfOfFundID)
	}
}

// TestOrderReservation_ReleaseNotActive_AndFullSettle covers the order-backed
// reservation lifecycle's not-active release branch and the full-settle
// transition to "settled".
func TestOrderReservation_ReleaseNotActive_AndFullSettle(t *testing.T) {
	svc, _, h := newHoldingReservationFixture(t)
	uid := uint64(1)

	// Reserve for a sell order, then release it; a second release is a no-op.
	if _, err := svc.Reserve(context.Background(), model.OwnerClient, &uid, "stock", h.SecurityID, 3001, 5); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := svc.Release(context.Background(), 3001); err != nil {
		t.Fatalf("first release: %v", err)
	}
	out, err := svc.Release(context.Background(), 3001)
	if err != nil || out.ReleasedQuantity != 0 {
		t.Fatalf("second release should be no-op, got %+v err=%v", out, err)
	}

	// A separate order: PartialSettle the whole reservation → status settled.
	if _, err := svc.Reserve(context.Background(), model.OwnerClient, &uid, "stock", h.SecurityID, 3002, 4); err != nil {
		t.Fatalf("reserve 2: %v", err)
	}
	res, err := svc.PartialSettle(context.Background(), 3002, 88001, 4)
	if err != nil {
		t.Fatalf("partial settle: %v", err)
	}
	if res.SettledQuantity != 4 {
		t.Errorf("settled = %d, want 4", res.SettledQuantity)
	}
	// Settling more against the now-settled reservation is rejected.
	if _, err := svc.PartialSettle(context.Background(), 3002, 88002, 1); err == nil {
		t.Errorf("expected rejection settling a fully-settled reservation")
	}
}
