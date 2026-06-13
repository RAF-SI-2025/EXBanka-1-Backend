package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestMintContractFromAcceptedNegotiation_HappyPath drives the full
// contract-formation saga: seller-share reservation + buyer-premium
// reserve/settle + seller credit, then post-saga notify/relist.
func TestMintContractFromAcceptedNegotiation_HappyPath(t *testing.T) {
	fx := newAcceptSagaFixture(t)

	buyerUID := uint64(fx.buyerID) // 55, the bidder
	neg := &model.OTCNegotiation{
		ParentOfferID:         fx.offer.ID,
		BidderOwnerType:       model.OwnerClient,
		BidderOwnerID:         &buyerUID,
		BidderAccountID:       5001, // BUYER-RSD
		Quantity:              decimal.NewFromInt(10),
		StrikePrice:           decimal.NewFromInt(5000),
		Premium:               decimal.NewFromInt(50000),
		SettlementDate:        time.Now().UTC().AddDate(0, 0, 7),
		Status:                model.OTCNegotiationStatusAccepted,
		LastActionByOwnerType: "client",
		LastActionByOwnerID:   &buyerUID,
	}
	contract, err := fx.svc.MintContractFromAcceptedNegotiation(context.Background(), MintFromNegotiationInput{
		Parent:             fx.offer,
		Negotiation:        neg,
		AcceptorOwnerType:  model.OwnerClient,
		AcceptorOwnerID:    &buyerUID, // the bidder accepts, binding their RSD account
		AcceptorAccountID:  5001,
		ActorPrincipalType: "client",
		ActorPrincipalID:   buyerUID,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if contract == nil || contract.ID == 0 {
		t.Fatalf("expected a minted contract")
	}
	if contract.Status != model.OptionContractStatusActive {
		t.Errorf("status = %s, want ACTIVE", contract.Status)
	}
	sellerUID := uint64(fx.sellerID)
	if contract.SellerOwnerID == nil || *contract.SellerOwnerID != sellerUID {
		t.Errorf("seller = %v, want %d", contract.SellerOwnerID, sellerUID)
	}
	if contract.BuyerAccountID != 5001 || contract.SellerAccountID != 6001 {
		t.Errorf("accounts buyer=%d seller=%d, want 5001/6001", contract.BuyerAccountID, contract.SellerAccountID)
	}
	// Premium settle happened on the buyer's reservation and seller was credited.
	if fx.accounts.settleCalls == 0 {
		t.Errorf("expected at least one premium settle")
	}
	// Both parties were notified of OTC_CONTRACT_CREATED.
	var created int
	for _, n := range fx.notifier.notifs {
		if n.Type == "OTC_CONTRACT_CREATED" {
			created++
		}
	}
	if created != 2 {
		t.Errorf("expected 2 contract-created notifications, got %d", created)
	}
}

func TestMintContractFromAcceptedNegotiation_SettlementInPast(t *testing.T) {
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
		SettlementDate:  time.Now().UTC().AddDate(0, 0, -2), // in the past
		Status:          model.OTCNegotiationStatusAccepted,
	}
	_, err := fx.svc.MintContractFromAcceptedNegotiation(context.Background(), MintFromNegotiationInput{
		Parent: fx.offer, Negotiation: neg,
		AcceptorOwnerType: model.OwnerClient, AcceptorOwnerID: &buyerUID, AcceptorAccountID: 5001,
		ActorPrincipalType: "client", ActorPrincipalID: buyerUID,
	})
	if err == nil {
		t.Fatal("expected error for a past settlement date")
	}
}

func TestMintContractFromAcceptedNegotiation_NilParent(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	_, err := fx.svc.MintContractFromAcceptedNegotiation(context.Background(), MintFromNegotiationInput{
		Parent: nil, Negotiation: nil,
	})
	if err == nil {
		t.Fatal("expected error for nil parent/negotiation")
	}
}
