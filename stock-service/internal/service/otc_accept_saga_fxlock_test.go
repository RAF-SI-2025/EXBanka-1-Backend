package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestBuildAcceptSaga_Recovery_ReusesLockedPremium_NoReconvert is the regression
// for audit #2 (FX rate-drift on local saga recovery): when a cross-currency
// accept saga is rebuilt on crash-recovery and the contract already carries the
// buyer-side premium locked at first accept, the saga MUST reuse it and NOT call
// exchange.Convert again (re-converting at a drifted rate would settle a
// different amount than the original hold reserved). We prove "Convert not
// called" by arming the exchange stub to fail if invoked.
func TestBuildAcceptSaga_Recovery_ReusesLockedPremium_NoReconvert(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	buyerUID := uint64(fx.buyerID)
	sellerUID := uint64(fx.sellerID)

	c := &model.OptionContract{
		ID:              999, // non-zero ⇒ recovery (contract already persisted)
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &buyerUID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &sellerUID,
		StockID:         fx.stockID,
		Ticker:          fx.offer.Ticker,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(5000),
		PremiumPaid:     decimal.NewFromInt(50000),
		PremiumCurrency: "RSD",
		BuyerAccountID:  5002, // BUYER-EUR → cross-currency vs the RSD premium
		SellerAccountID: 6001, // SELLER-RSD
		SettlementDate:  time.Now().UTC().AddDate(0, 0, 7),
		// The buyer-side premium locked at the first accept (the value the original
		// attempt reserved against).
		BuyerPremiumAmount:   decimal.NewFromInt(417),
		BuyerPremiumCurrency: "EUR",
	}

	fx.exchange.failNext = errors.New("exchange.Convert must NOT be called on recovery")
	if _, _, err := fx.svc.buildAcceptSaga(context.Background(), "sid-recovery", c); err != nil {
		t.Fatalf("recovery build must reuse the locked premium (no re-convert), got: %v", err)
	}
}
