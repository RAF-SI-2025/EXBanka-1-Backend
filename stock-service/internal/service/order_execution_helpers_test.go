package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func newTestEngine() *OrderExecutionEngine {
	return NewOrderExecutionEngine(
		context.Background(),
		&fakeBaseCtxOrderRepo{},
		&fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{},
		&fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{},
		&fakeBaseCtxFillHandler{},
	)
}

// ---------------- getExecutionPrice ----------------

func TestEngine_GetExecutionPrice_MarketBuyUsesAsk(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	order := &model.Order{OrderType: "market", Direction: "buy"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(105)) {
		t.Errorf("got %s want 105", got)
	}
}

func TestEngine_GetExecutionPrice_MarketSellUsesBid(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	order := &model.Order{OrderType: "market", Direction: "sell"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(95)) {
		t.Errorf("got %s want 95", got)
	}
}

func TestEngine_GetExecutionPrice_HighZeroFallsBackToPrice(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.Zero, Low: decimal.Zero}
	order := &model.Order{OrderType: "market", Direction: "buy"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100", got)
	}
}

func TestEngine_GetExecutionPrice_LimitBuyClampsToAsk(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	limit := decimal.NewFromInt(110)
	order := &model.Order{OrderType: "limit", Direction: "buy", LimitValue: &limit}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(105)) {
		t.Errorf("got %s want 105 (ask)", got)
	}
}

func TestEngine_GetExecutionPrice_LimitSellClampsToBid(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	limit := decimal.NewFromInt(90)
	order := &model.Order{OrderType: "limit", Direction: "sell", LimitValue: &limit}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(95)) {
		t.Errorf("got %s want 95 (bid)", got)
	}
}

func TestEngine_GetExecutionPrice_LimitWithinSpreadBuy(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	limit := decimal.NewFromInt(102)
	order := &model.Order{OrderType: "limit", Direction: "buy", LimitValue: &limit}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(102)) {
		t.Errorf("got %s want 102", got)
	}
}

func TestEngine_GetExecutionPrice_LimitNoValueFallsBack(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	order := &model.Order{OrderType: "limit", Direction: "buy"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100", got)
	}
}

func TestEngine_GetExecutionPrice_UnknownOrderType(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100)}
	order := &model.Order{OrderType: "weird"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100", got)
	}
}

// ---------------- isOrderTriggered ----------------

func TestEngine_IsOrderTriggered_MarketAlwaysTrue(t *testing.T) {
	e := newTestEngine()
	if !e.isOrderTriggered(&model.Order{OrderType: "market"}) {
		t.Error("market should be triggered")
	}
	if !e.isOrderTriggered(&model.Order{OrderType: "limit"}) {
		t.Error("limit should be triggered")
	}
}

func TestEngine_IsOrderTriggered_UnknownTypeReturnsTrue(t *testing.T) {
	e := newTestEngine()
	if !e.isOrderTriggered(&model.Order{OrderType: "weird"}) {
		t.Error("unknown order type should fall through to true")
	}
}

// TestEngine_MarkDone_RemainingPortionsZero exercises the executeOrder
// branch where remaining portions is 0 → markDone runs.
func TestEngine_ExecuteOrder_RemainingZero_TriggersMarkDone(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	uid := uint64(7)
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID: 42, Status: "approved", IsDone: false,
		Direction: "buy", RemainingPortions: 0, Quantity: 5,
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 7, OrderType: "market", ContractSize: 1,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID: 7, Volume: 100, Price: decimal.NewFromInt(100),
		High: decimal.NewFromInt(100), Low: decimal.NewFromInt(100),
	}}
	fillHandler := &fakeBaseCtxFillHandler{filled: make(chan struct{}, 1)}
	engine := NewOrderExecutionEngine(
		baseCtx, orderRepo, &fakeBaseCtxTxRepo{}, listingRepo,
		&fakeBaseCtxSettingRepo{}, fakeBaseCtxPublisher{}, fillHandler,
	)
	// Synchronous executeOrder invocation.
	engine.executeOrder(baseCtx, 42)
	// After markDone the order should be IsDone=true.
	if !orderRepo.order.IsDone {
		t.Errorf("expected IsDone=true after markDone")
	}
}

// ---------------- calculateWaitTime ----------------

func TestEngine_CalculateWaitTime_DefaultZeroVolume(t *testing.T) {
	e := newTestEngine()
	out := e.calculateWaitTime(0, 0, false)
	if out < 0 || out > 60 {
		t.Errorf("got %d (expected 0-60)", out)
	}
}

func TestEngine_CalculateWaitTime_AfterHoursAdds30Min(t *testing.T) {
	e := newTestEngine()
	out := e.calculateWaitTime(1000, 1, true)
	if out < 30*60 {
		t.Errorf("got %d (expected at least 1800s)", out)
	}
}
