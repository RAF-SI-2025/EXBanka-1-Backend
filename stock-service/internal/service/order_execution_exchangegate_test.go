package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// closedExchangeChecker reports every exchange closed and signals when the gate
// consults it, so the test can deterministically wait for the gate to be hit.
type closedExchangeChecker struct{ called chan struct{} }

func (c *closedExchangeChecker) IsExchangeOpen(uint64) (bool, error) {
	select {
	case c.called <- struct{}{}:
	default:
	}
	return false, nil
}

// TestEngine_ExecuteOrder_ClosedExchange_DoesNotFill verifies the exchange-open
// gate: when the listing's exchange is closed (the same predicate as is_open),
// the engine waits instead of filling — so a fill never happens while is_open is
// false. Without the gate the market order would fill immediately.
func TestEngine_ExecuteOrder_ClosedExchange_DoesNotFill(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uid := uint64(7)
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID: 42, Status: "approved", IsDone: false,
		Direction: "buy", RemainingPortions: 3, Quantity: 3,
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 7, OrderType: "market", ContractSize: 1,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID: 7, Volume: 1_000_000_000, Price: decimal.NewFromInt(100),
		High: decimal.NewFromInt(100), Low: decimal.NewFromInt(100),
	}}
	fill := &abortFillHandler{} // counts ProcessBuyFill; must stay 0
	txRepo := &abortTxRepo{}

	engine := NewOrderExecutionEngine(
		baseCtx, orderRepo, txRepo, listingRepo,
		&fakeBaseCtxSettingRepo{}, fakeBaseCtxPublisher{}, fill,
	)
	checker := &closedExchangeChecker{called: make(chan struct{}, 1)}
	engine.SetExchangeChecker(checker)

	done := make(chan struct{})
	go func() { engine.executeOrder(baseCtx, 42); close(done) }()

	select {
	case <-checker.called: // gate consulted → the order reached the open/closed check
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("exchange-open gate was never consulted")
	}
	cancel() // interrupt the closed-exchange wait
	<-done

	if fill.buyCalls != 0 {
		t.Errorf("closed exchange must not fill: got %d fill attempts", fill.buyCalls)
	}
	if txRepo.creates != 0 {
		t.Errorf("closed exchange must not create transactions: got %d", txRepo.creates)
	}
}
