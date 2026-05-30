package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestRecoverFillSaga_BuyForwardResumeOnce proves the no-human auto-resolve
// forward path for a stock buy fill: re-driving the fill saga completes it
// (settle + holding credit) and, invoked repeatedly under the same sagaID,
// neither double-settles nor double-credits (completed steps skipped on resume;
// the holding upsert is marker-guarded).
func TestRecoverFillSaga_BuyForwardResumeOnce(t *testing.T) {
	svc, mocks := buildPortfolioServiceWithSaga("USD", "USD")
	listing := stockListing(1, 100, 100.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)
	mocks.stockRepo.addStock(&model.Stock{ID: 100, Ticker: "AAPL", Name: "Apple Inc."})

	txn := &model.OrderTransaction{
		ID: 900, OrderID: 1, Quantity: 10,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(1000),
	}
	_ = mocks.txRepo.Create(txn)
	order := &model.Order{
		ID: 1, OwnerType: model.OwnerClient, OwnerID: ptrU64(77),
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", Quantity: 10, AccountID: 1,
	}

	for i := 0; i < 2; i++ {
		if err := svc.RecoverFillSaga(context.Background(), "fill-saga-1", order, txn, false); err != nil {
			t.Fatalf("RecoverFillSaga #%d: %v", i, err)
		}
	}

	holding, err := mocks.holdingRepo.GetByOwnerAndSecurity(model.OwnerClient, ptrU64(77), "stock", 100)
	if err != nil {
		t.Fatalf("holding not found: %v", err)
	}
	if holding.Quantity != 10 {
		t.Fatalf("buy fill recovery credited holding %d times: quantity=%d want 10", holding.Quantity/10, holding.Quantity)
	}
	if len(mocks.fillClient.partialSettleCalls) != 1 {
		t.Fatalf("recovery double-settled: %d PartialSettleReservation calls, want 1", len(mocks.fillClient.partialSettleCalls))
	}
}
