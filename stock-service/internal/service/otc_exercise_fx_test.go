package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestExerciseContract_CrossCurrency_FXConvertsStrike exercises buildExerciseSaga's
// cross-currency branch: the buyer's account currency (EUR) differs from the
// strike/seller currency (RSD), so the strike is FX-converted at exercise.
func TestExerciseContract_CrossCurrency_FXConvertsStrike(t *testing.T) {
	fx := newAcceptSagaFixture(t)
	// Tell the exchange fake what one strike-leg converts to in the buyer's ccy.
	fx.exchange.rate = "117"
	fx.exchange.convert = "585000" // 5000 RSD strike × qty 10 → 50000 RSD → ~585000? value only needs to parse

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
		SagaID: uuid.NewString(), PremiumPaidAt: time.Now().UTC(),
		BuyerAccountID:  5002, // BUYER-EUR → triggers cross-currency strike conversion
		SellerAccountID: 6001, // SELLER-RSD
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("mint contract: %v", err)
	}
	if _, err := fx.holdingResSvc.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &sellerUID, "stock", fx.stockID, c.ID, c.Quantity.IntPart()); err != nil {
		t.Fatalf("reserve seller holding: %v", err)
	}

	exercised, err := fx.svc.ExerciseContract(context.Background(), ExerciseInput{
		ContractID: c.ID, ActorUserID: fx.buyerID, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if exercised.Status != model.OptionContractStatusExercised {
		t.Errorf("status = %s, want EXERCISED", exercised.Status)
	}
	// The cross-currency path locked the converted buyer-side strike on the contract.
	if !exercised.BuyerStrikeAmount.IsPositive() || exercised.BuyerStrikeCurrency != "EUR" {
		t.Errorf("buyer-side strike not locked: amount=%s ccy=%s", exercised.BuyerStrikeAmount, exercised.BuyerStrikeCurrency)
	}
}
