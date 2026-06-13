package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestMintContract_CrossCurrencyPremium exercises buildAcceptSaga's FX branch:
// the buyer pays the premium from a EUR account while the seller (and thus the
// premium) is denominated in RSD, so the premium is FX-converted.
func TestMintContract_CrossCurrencyPremium(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	fx.exchange.rate = "117"
	fx.exchange.convert = "427" // converted premium in the buyer's currency

	buyerUID := uint64(fx.buyerID)
	neg := &model.OTCNegotiation{
		ParentOfferID:   fx.offer.ID,
		BidderOwnerType: model.OwnerClient,
		BidderOwnerID:   &buyerUID,
		BidderAccountID: 5002, // BUYER-EUR
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(5000),
		Premium:         decimal.NewFromInt(50000),
		SettlementDate:  time.Now().UTC().AddDate(0, 0, 7),
		Status:          model.OTCNegotiationStatusAccepted,
	}

	contract, err := fx.svc.MintContractFromAcceptedNegotiation(context.Background(), MintFromNegotiationInput{
		Parent: fx.offer, Negotiation: neg,
		AcceptorOwnerType: model.OwnerClient, AcceptorOwnerID: &buyerUID, AcceptorAccountID: 5002,
		ActorPrincipalType: "client", ActorPrincipalID: buyerUID,
	})
	if err != nil {
		t.Fatalf("mint cross-currency: %v", err)
	}
	if contract.BuyerAccountID != 5002 {
		t.Errorf("buyer account = %d, want 5002 (EUR)", contract.BuyerAccountID)
	}
	// Premium currency is the seller's (RSD); the buyer-side FX lock differs.
	if contract.PremiumCurrency != "RSD" {
		t.Errorf("premium currency = %s, want RSD (seller account currency)", contract.PremiumCurrency)
	}
	// A premium reservation + settle happened on the buyer side.
	if fx.accounts.reserveCalls == 0 || fx.accounts.settleCalls == 0 {
		t.Errorf("expected premium reserve+settle, got reserve=%d settle=%d", fx.accounts.reserveCalls, fx.accounts.settleCalls)
	}
}
