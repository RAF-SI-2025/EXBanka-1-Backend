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

// Fills execute at the live price (listing.Price) for both sides — the
// single-price sim has no bid/ask spread; High/Low are display-only day-range
// stats (see sideQuotes).
func TestEngine_GetExecutionPrice_MarketBuyUsesLivePrice(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	order := &model.Order{OrderType: "market", Direction: "buy"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100 (live price)", got)
	}
}

func TestEngine_GetExecutionPrice_MarketSellUsesLivePrice(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	order := &model.Order{OrderType: "market", Direction: "sell"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100 (live price)", got)
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

// Limit orders fill at the live price. The price condition is enforced by
// isOrderTriggered — getExecutionPrice is only reached when the limit is already
// satisfied, so it returns the live quote rather than the (more favourable) limit
// value the user typed in.
func TestEngine_GetExecutionPrice_LimitBuyReturnsLivePrice(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	limit := decimal.NewFromInt(110)
	order := &model.Order{OrderType: "limit", Direction: "buy", LimitValue: &limit}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100 (live price)", got)
	}
}

func TestEngine_GetExecutionPrice_LimitSellReturnsLivePrice(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	limit := decimal.NewFromInt(90)
	order := &model.Order{OrderType: "limit", Direction: "sell", LimitValue: &limit}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100 (live price)", got)
	}
}

func TestEngine_GetExecutionPrice_LimitNoValueFallsBackToQuote(t *testing.T) {
	e := newTestEngine()
	listing := &model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95)}
	order := &model.Order{OrderType: "limit", Direction: "buy"}
	got := e.getExecutionPrice(order, listing)
	if !got.Equal(decimal.NewFromInt(100)) {
		t.Errorf("got %s want 100 (live price)", got)
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
}

func TestEngine_IsOrderTriggered_UnknownTypeReturnsTrue(t *testing.T) {
	e := newTestEngine()
	if !e.isOrderTriggered(&model.Order{OrderType: "weird"}) {
		t.Error("unknown order type should fall through to true")
	}
}

// ---------------- isOrderTriggered: limit ----------------

func makeListing(price, high, low int64) *model.Listing {
	return &model.Listing{
		ID: 7, Price: decimal.NewFromInt(price),
		High: decimal.NewFromInt(high), Low: decimal.NewFromInt(low),
	}
}

func TestEngine_IsOrderTriggered_BuyLimit_PriceAboveLimit_NotTriggered(t *testing.T) {
	listing := makeListing(100, 120, 80)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	limit := decimal.NewFromInt(90) // willing to buy at <= 90, live price is 100
	order := &model.Order{OrderType: "limit", Direction: "buy", ListingID: 7, LimitValue: &limit}
	if e.isOrderTriggered(order) {
		t.Error("buy-limit with live price above limit should NOT be triggered")
	}
}

func TestEngine_IsOrderTriggered_BuyLimit_AskAtLimit_Triggered(t *testing.T) {
	listing := makeListing(100, 110, 80)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	limit := decimal.NewFromInt(110)
	order := &model.Order{OrderType: "limit", Direction: "buy", ListingID: 7, LimitValue: &limit}
	if !e.isOrderTriggered(order) {
		t.Error("buy-limit with ask equal to limit should be triggered")
	}
}

func TestEngine_IsOrderTriggered_SellLimit_PriceBelowLimit_NotTriggered(t *testing.T) {
	listing := makeListing(100, 120, 80)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	limit := decimal.NewFromInt(110) // willing to sell at >= 110, live price is 100
	order := &model.Order{OrderType: "limit", Direction: "sell", ListingID: 7, LimitValue: &limit}
	if e.isOrderTriggered(order) {
		t.Error("sell-limit with live price below limit should NOT be triggered")
	}
}

func TestEngine_IsOrderTriggered_SellLimit_BidAtLimit_Triggered(t *testing.T) {
	listing := makeListing(100, 120, 90)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	limit := decimal.NewFromInt(90)
	order := &model.Order{OrderType: "limit", Direction: "sell", ListingID: 7, LimitValue: &limit}
	if !e.isOrderTriggered(order) {
		t.Error("sell-limit with bid equal to limit should be triggered")
	}
}

func TestEngine_IsOrderTriggered_Limit_NoLimitValue_NotTriggered(t *testing.T) {
	listing := makeListing(100, 105, 95)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	order := &model.Order{OrderType: "limit", Direction: "buy", ListingID: 7}
	if e.isOrderTriggered(order) {
		t.Error("limit without LimitValue should NOT be triggered (defensive)")
	}
}

// ---------------- isOrderTriggered: stop_limit ----------------

func TestEngine_IsOrderTriggered_StopLimit_Buy_StopNotCrossed_NotTriggered(t *testing.T) {
	// High=110, stop=120 (not crossed yet)
	listing := makeListing(100, 110, 95)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	stop, limit := decimal.NewFromInt(120), decimal.NewFromInt(130)
	order := &model.Order{OrderType: "stop_limit", Direction: "buy", ListingID: 7, StopValue: &stop, LimitValue: &limit}
	if e.isOrderTriggered(order) {
		t.Error("stop-limit buy with stop not crossed should NOT be triggered")
	}
}

func TestEngine_IsOrderTriggered_StopLimit_Buy_StopCrossed_LimitTooLow_NotTriggered(t *testing.T) {
	// High=130 (stop crossed), but live price 130 > limit 120 (buy limit not met)
	listing := makeListing(130, 130, 95)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	stop, limit := decimal.NewFromInt(120), decimal.NewFromInt(120)
	order := &model.Order{OrderType: "stop_limit", Direction: "buy", ListingID: 7, StopValue: &stop, LimitValue: &limit}
	if e.isOrderTriggered(order) {
		t.Error("stop-limit buy with stop crossed but live price above limit should NOT be triggered")
	}
}

func TestEngine_IsOrderTriggered_StopLimit_Buy_StopCrossed_LimitMet_Triggered(t *testing.T) {
	// High=122 (stop=120 crossed), limit=130 >= ask=122
	listing := makeListing(100, 122, 95)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	stop, limit := decimal.NewFromInt(120), decimal.NewFromInt(130)
	order := &model.Order{OrderType: "stop_limit", Direction: "buy", ListingID: 7, StopValue: &stop, LimitValue: &limit}
	if !e.isOrderTriggered(order) {
		t.Error("stop-limit buy with stop crossed AND ask within limit should be triggered")
	}
}

func TestEngine_IsOrderTriggered_StopLimit_Sell_StopNotCrossed_NotTriggered(t *testing.T) {
	// Low=85, stop=80 — not crossed yet (price hasn't fallen far enough)
	listing := makeListing(100, 110, 85)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	stop, limit := decimal.NewFromInt(80), decimal.NewFromInt(70)
	order := &model.Order{OrderType: "stop_limit", Direction: "sell", ListingID: 7, StopValue: &stop, LimitValue: &limit}
	if e.isOrderTriggered(order) {
		t.Error("stop-limit sell with stop not crossed should NOT be triggered")
	}
}

func TestEngine_IsOrderTriggered_StopLimit_Sell_StopCrossed_LimitTooHigh_NotTriggered(t *testing.T) {
	// Low=70 (stop=80 crossed), but live price 70 < limit 90 (sell limit not met)
	listing := makeListing(70, 110, 70)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	stop, limit := decimal.NewFromInt(80), decimal.NewFromInt(90)
	order := &model.Order{OrderType: "stop_limit", Direction: "sell", ListingID: 7, StopValue: &stop, LimitValue: &limit}
	if e.isOrderTriggered(order) {
		t.Error("stop-limit sell with stop crossed but live price below limit should NOT be triggered")
	}
}

func TestEngine_IsOrderTriggered_StopLimit_Sell_StopCrossed_LimitMet_Triggered(t *testing.T) {
	// Low=75 (stop=80 crossed), limit=70 <= bid=75
	listing := makeListing(100, 110, 75)
	e := NewOrderExecutionEngine(context.Background(),
		&fakeBaseCtxOrderRepo{}, &fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{l: listing}, &fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{}, &fakeBaseCtxFillHandler{})
	stop, limit := decimal.NewFromInt(80), decimal.NewFromInt(70)
	order := &model.Order{OrderType: "stop_limit", Direction: "sell", ListingID: 7, StopValue: &stop, LimitValue: &limit}
	if !e.isOrderTriggered(order) {
		t.Error("stop-limit sell with stop crossed AND bid within limit should be triggered")
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
