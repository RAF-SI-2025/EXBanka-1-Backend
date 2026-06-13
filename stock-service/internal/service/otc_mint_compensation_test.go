package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestMintContract_FailsAtSellerCredit_Compensates makes the credit_premium_seller
// step fail so buildAcceptSaga's executor walks the prior steps' Backward
// closures (release the buyer's premium reservation, credit the premium back,
// release the seller's share reservation, delete the contract row).
func TestMintContract_FailsAtSellerCredit_Compensates(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	// Fail the seller-credit (last money step) once.
	fx.accounts.failCreditOnce = errors.New("seller credit failed")

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

	_, err := fx.svc.MintContractFromAcceptedNegotiation(context.Background(), MintFromNegotiationInput{
		Parent: fx.offer, Negotiation: neg,
		AcceptorOwnerType: model.OwnerClient, AcceptorOwnerID: &buyerUID, AcceptorAccountID: 5001,
		ActorPrincipalType: "client", ActorPrincipalID: buyerUID,
	})
	if err == nil {
		t.Fatal("expected mint to fail at the seller-credit step")
	}

	// Compensation released the buyer's premium reservation.
	if fx.accounts.releaseCalls == 0 {
		t.Errorf("expected the premium reservation to be released on compensation")
	}

	// The seller's share reservation was released back: holding reserved → 0.
	sellerUID := uint64(fx.sellerID)
	h, herr := fx.holdings.GetByOwnerAndSecurity(model.OwnerClient, &sellerUID, "stock", fx.stockID)
	if herr != nil {
		t.Fatalf("seller holding: %v", herr)
	}
	if h.ReservedQuantity != 0 {
		t.Errorf("seller reserved = %d, want 0 after compensation", h.ReservedQuantity)
	}
}
