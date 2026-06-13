package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func decPtr(v int64) *decimal.Decimal {
	d := decimal.NewFromInt(v)
	return &d
}

func TestStopConditionMet(t *testing.T) {
	// nil stop → always triggered.
	if !stopConditionMet(&model.Order{Direction: "buy"}, &model.Listing{}) {
		t.Error("nil stop should be triggered")
	}
	// buy stop triggers when high >= stop.
	buy := &model.Order{Direction: "buy", StopValue: decPtr(100)}
	if !stopConditionMet(buy, &model.Listing{High: decimal.NewFromInt(101)}) {
		t.Error("buy stop should trigger when high >= stop")
	}
	if stopConditionMet(buy, &model.Listing{High: decimal.NewFromInt(99)}) {
		t.Error("buy stop should not trigger when high < stop")
	}
	// sell stop triggers when low <= stop.
	sell := &model.Order{Direction: "sell", StopValue: decPtr(100)}
	if !stopConditionMet(sell, &model.Listing{Low: decimal.NewFromInt(99)}) {
		t.Error("sell stop should trigger when low <= stop")
	}
	if stopConditionMet(sell, &model.Listing{Low: decimal.NewFromInt(101)}) {
		t.Error("sell stop should not trigger when low > stop")
	}
}

func TestLimitConditionMet(t *testing.T) {
	// nil limit → never.
	if limitConditionMet(&model.Order{Direction: "buy"}, &model.Listing{Price: decimal.NewFromInt(100)}) {
		t.Error("nil limit should not be met")
	}
	// buy limit: ask <= limit.
	buy := &model.Order{Direction: "buy", LimitValue: decPtr(100)}
	if !limitConditionMet(buy, &model.Listing{Price: decimal.NewFromInt(90)}) {
		t.Error("buy limit met when ask <= limit")
	}
	if limitConditionMet(buy, &model.Listing{Price: decimal.NewFromInt(110)}) {
		t.Error("buy limit not met when ask > limit")
	}
	// sell limit: bid >= limit.
	sell := &model.Order{Direction: "sell", LimitValue: decPtr(100)}
	if !limitConditionMet(sell, &model.Listing{Price: decimal.NewFromInt(110)}) {
		t.Error("sell limit met when bid >= limit")
	}
}

func TestExecPriceAllowed(t *testing.T) {
	// market always allowed.
	if !execPriceAllowed(&model.Order{OrderType: "market"}, decimal.NewFromInt(999)) {
		t.Error("market should allow any price")
	}
	// stop (non-limit) always allowed.
	if !execPriceAllowed(&model.Order{OrderType: "stop"}, decimal.NewFromInt(999)) {
		t.Error("stop should allow any price")
	}
	// limit with nil LimitValue → not allowed.
	if execPriceAllowed(&model.Order{OrderType: "limit"}, decimal.NewFromInt(10)) {
		t.Error("limit with nil LimitValue should not be allowed")
	}
	// buy limit: exec <= limit.
	buy := &model.Order{OrderType: "limit", Direction: "buy", LimitValue: decPtr(100)}
	if !execPriceAllowed(buy, decimal.NewFromInt(90)) || execPriceAllowed(buy, decimal.NewFromInt(110)) {
		t.Error("buy limit exec price gate wrong")
	}
	// sell stop_limit: exec >= limit.
	sell := &model.Order{OrderType: "stop_limit", Direction: "sell", LimitValue: decPtr(100)}
	if !execPriceAllowed(sell, decimal.NewFromInt(110)) || execPriceAllowed(sell, decimal.NewFromInt(90)) {
		t.Error("sell stop_limit exec price gate wrong")
	}
}

func TestSideQuotes(t *testing.T) {
	// Non-zero price → both sides are the live price.
	ask, bid := sideQuotes(&model.Listing{Price: decimal.NewFromInt(100), High: decimal.NewFromInt(120), Low: decimal.NewFromInt(80)})
	if !ask.Equal(decimal.NewFromInt(100)) || !bid.Equal(decimal.NewFromInt(100)) {
		t.Errorf("non-zero price quotes = (%s,%s), want (100,100)", ask, bid)
	}
	// Zero price, non-zero high/low → fall back to high/low.
	ask, bid = sideQuotes(&model.Listing{Price: decimal.Zero, High: decimal.NewFromInt(50), Low: decimal.NewFromInt(40)})
	if !ask.Equal(decimal.NewFromInt(50)) || !bid.Equal(decimal.NewFromInt(40)) {
		t.Errorf("zero-price quotes = (%s,%s), want (50,40)", ask, bid)
	}
	// All zero → zero.
	ask, bid = sideQuotes(&model.Listing{})
	if !ask.IsZero() || !bid.IsZero() {
		t.Errorf("all-zero quotes = (%s,%s), want (0,0)", ask, bid)
	}
}

func newTriggerEngine(listings *mockListingRepo) *OrderExecutionEngine {
	return NewOrderExecutionEngine(
		context.Background(),
		&fakeBaseCtxOrderRepo{},
		&fakeBaseCtxTxRepo{},
		listings,
		&fakeBaseCtxSettingRepo{},
		fakeBaseCtxPublisher{},
		&fakeBaseCtxFillHandler{},
	)
}

func TestIsOrderTriggered(t *testing.T) {
	listings := newMockListingRepo()
	listings.addListing(&model.Listing{ID: 1, Price: decimal.NewFromInt(90), High: decimal.NewFromInt(90), Low: decimal.NewFromInt(90)})
	e := newTriggerEngine(listings)

	// market → always.
	if !e.isOrderTriggered(&model.Order{OrderType: "market"}) {
		t.Error("market order always triggered")
	}
	// limit buy at 100 with ask 90 → triggered.
	if !e.isOrderTriggered(&model.Order{OrderType: "limit", Direction: "buy", ListingID: 1, LimitValue: decPtr(100)}) {
		t.Error("buy limit should trigger when ask <= limit")
	}
	// limit with missing listing → false.
	if e.isOrderTriggered(&model.Order{OrderType: "limit", Direction: "buy", ListingID: 999, LimitValue: decPtr(100)}) {
		t.Error("missing listing → not triggered")
	}
	// stop buy at 80 with high (price 90) >= 80 → triggered.
	if !e.isOrderTriggered(&model.Order{OrderType: "stop", Direction: "buy", ListingID: 1, StopValue: decPtr(80)}) {
		t.Error("buy stop should trigger")
	}
	// stop_limit: both stop AND limit must hold. Stop 80 (price 90 >= 80) AND
	// buy limit 100 (ask 90 <= 100) → triggered.
	if !e.isOrderTriggered(&model.Order{OrderType: "stop_limit", Direction: "buy", ListingID: 1, StopValue: decPtr(80), LimitValue: decPtr(100)}) {
		t.Error("stop_limit should trigger when stop and limit both met")
	}
	// stop_limit with limit not met (buy limit 50, ask 90) → false.
	if e.isOrderTriggered(&model.Order{OrderType: "stop_limit", Direction: "buy", ListingID: 1, StopValue: decPtr(80), LimitValue: decPtr(50)}) {
		t.Error("stop_limit should not trigger when limit unmet")
	}
}
