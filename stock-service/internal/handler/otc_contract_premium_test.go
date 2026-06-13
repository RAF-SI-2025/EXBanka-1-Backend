package handler

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestPeerContractToUnifiedProto_ExposesPremium asserts the cross-bank (remote)
// contract DTO surfaces premium_paid / premium_currency. These are captured from
// the originating negotiation at RecordOptionContract time and were previously
// omitted from the unified projection → cross-bank contracts came back with no
// premium ("premium missing from dto for contract").
func TestPeerContractToUnifiedProto_ExposesPremium(t *testing.T) {
	native := "cb:tx-1:0"
	bbc, sbc := "222", "111"
	dir := "DEBIT"
	pidx := int32(0)
	negR := int64(111)
	negN := "neg-1"
	bID, sID := "client-7", "client-9"
	p := &model.OptionContract{
		ID:                        1,
		RoutingNumber:             222,
		NativeID:                  &native,
		BuyerBankCode:             &bbc,
		SellerBankCode:            &sbc,
		Ticker:                    "AAPL",
		Quantity:                  decimal.NewFromInt(10),
		StrikePrice:               decimal.NewFromInt(150),
		PremiumPaid:               decimal.NewFromInt(40),
		PremiumCurrency:           "USD",
		StrikeCurrency:            "USD",
		Status:                    "active",
		SettlementDate:            time.Now().Add(24 * time.Hour),
		CreatedAt:                 time.Now(),
		RemotePostingIndex:        &pidx,
		RemoteNegotiationRouting:  &negR,
		RemoteNegotiationNativeID: &negN,
		RemoteDirection:           &dir,
		RemoteBuyerID:             &bID,
		RemoteSellerID:            &sID,
	}
	got := peerContractToUnifiedProto(p)
	if got.GetPremiumPaid() != "40" {
		t.Errorf("premium_paid = %q want 40", got.GetPremiumPaid())
	}
	if got.GetPremiumCurrency() != "USD" {
		t.Errorf("premium_currency = %q want USD", got.GetPremiumCurrency())
	}
}
