package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	contract "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// --- test doubles for the publish-payload test ---

// capturingPublisher records every PublishOrderFilled call it receives so the
// test can assert the resulting payload shape.
type capturingPublisher struct {
	mu     sync.Mutex
	events []map[string]any
	notifs []contract.GeneralNotificationMessage
}

func (p *capturingPublisher) PublishOrderFilled(_ context.Context, msg interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := msg.(map[string]any); ok {
		p.events = append(p.events, m)
	}
	return nil
}

func (p *capturingPublisher) PublishGeneralNotification(_ context.Context, msg contract.GeneralNotificationMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notifs = append(p.notifs, msg)
	return nil
}

func (p *capturingPublisher) snapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.events))
	copy(out, p.events)
	return out
}

func (p *capturingPublisher) notifSnapshot() []contract.GeneralNotificationMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]contract.GeneralNotificationMessage, len(p.notifs))
	copy(out, p.notifs)
	return out
}

// crossCurrencyFillHandler stamps cross-currency conversion fields on the
// OrderTransaction to mimic the behaviour of the real fill-saga's
// convert_amount step, then returns nil.
type crossCurrencyFillHandler struct {
	filled chan struct{}
	once   sync.Once
}

func (h *crossCurrencyFillHandler) signal() {
	h.once.Do(func() { close(h.filled) })
}

func (h *crossCurrencyFillHandler) ProcessBuyFill(_ *model.Order, txn *model.OrderTransaction) error {
	native := decimal.NewFromFloat(250.0000)
	converted := decimal.NewFromFloat(29375.0000)
	fx := decimal.NewFromFloat(117.5000)
	txn.NativeAmount = &native
	txn.NativeCurrency = "USD"
	txn.ConvertedAmount = &converted
	txn.AccountCurrency = "RSD"
	txn.FxRate = &fx
	h.signal()
	return nil
}

func (h *crossCurrencyFillHandler) ProcessSellFill(_ *model.Order, _ *model.OrderTransaction) error {
	h.signal()
	return nil
}

func (h *crossCurrencyFillHandler) ReleaseResidualReservation(_ context.Context, _ uint64) error {
	return nil
}

// failingFillHandler always returns an error, simulating a failed saga step.
type failingFillHandler struct {
	attempts int32
	mu       sync.Mutex
	called   chan struct{}
	once     sync.Once
}

func (h *failingFillHandler) bump() {
	h.mu.Lock()
	h.attempts++
	h.mu.Unlock()
	h.once.Do(func() { close(h.called) })
}

func (h *failingFillHandler) ProcessBuyFill(_ *model.Order, _ *model.OrderTransaction) error {
	h.bump()
	return errors.New("fill saga failed")
}

func (h *failingFillHandler) ProcessSellFill(_ *model.Order, _ *model.OrderTransaction) error {
	h.bump()
	return errors.New("fill saga failed")
}

func (h *failingFillHandler) ReleaseResidualReservation(_ context.Context, _ uint64) error {
	return nil
}

// TestExecuteOrder_KafkaPayload_IncludesSagaFields verifies that a successful
// fill produces a Kafka event with the new saga-correlation and
// currency-conversion fields, and that the publish happens synchronously
// after the fill handler returns success.
func TestExecuteOrder_KafkaPayload_IncludesSagaFields(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	publishUID := uint64(4242)
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID:                101,
		OwnerType:         model.OwnerClient,
		OwnerID:           &publishUID,
		SagaID:            "saga-abc-123",
		Status:            "approved",
		IsDone:            false,
		Direction:         "buy",
		SecurityType:      "stock",
		Ticker:            "AAPL",
		RemainingPortions: 1,
		Quantity:          1,
		AllOrNone:         true,
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         9,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID:     9,
		Volume: 1_000_000,
		Price:  decimal.NewFromInt(250),
		High:   decimal.NewFromInt(250),
		Low:    decimal.NewFromInt(250),
	}}
	txRepo := &fakeBaseCtxTxRepo{created: make(chan uint64, 1)}
	fillHandler := &crossCurrencyFillHandler{filled: make(chan struct{})}
	pub := &capturingPublisher{}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeBaseCtxSettingRepo{},
		pub, fillHandler,
	)

	engine.StartOrderExecution(context.Background(), 101)

	// Wait for the fill handler to fire.
	select {
	case <-fillHandler.filled:
	case <-time.After(3 * time.Second):
		t.Fatalf("fill handler never invoked within 3s")
	}

	// Give executeOrder time to reach the publish step and return.
	deadline := time.Now().Add(3 * time.Second)
	var events []map[string]any
	for time.Now().Before(deadline) {
		events = pub.snapshot()
		if len(events) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(events) < 1 {
		t.Fatalf("expected >=1 kafka event after successful fill, got %d", len(events))
	}

	ev := events[0]

	// Required saga-correlation fields.
	if got, ok := ev["saga_id"].(string); !ok || got != "saga-abc-123" {
		t.Errorf("saga_id: want %q, got %v", "saga-abc-123", ev["saga_id"])
	}
	if _, ok := ev["order_txn_id"]; !ok {
		t.Errorf("order_txn_id field missing from payload")
	}
	if got, ok := ev["order_id"].(uint64); !ok || got != 101 {
		t.Errorf("order_id: want 101, got %v", ev["order_id"])
	}

	// Currency-conversion audit fields (populated by the handler above).
	if got, ok := ev["native_amount"].(string); !ok || got != "250.0000" {
		t.Errorf("native_amount: want %q, got %v", "250.0000", ev["native_amount"])
	}
	if got, ok := ev["native_currency"].(string); !ok || got != "USD" {
		t.Errorf("native_currency: want %q, got %v", "USD", ev["native_currency"])
	}
	if got, ok := ev["converted_amount"].(string); !ok || got != "29375.0000" {
		t.Errorf("converted_amount: want %q, got %v", "29375.0000", ev["converted_amount"])
	}
	if got, ok := ev["account_currency"].(string); !ok || got != "RSD" {
		t.Errorf("account_currency: want %q, got %v", "RSD", ev["account_currency"])
	}
	if got, ok := ev["fx_rate"].(string); !ok || got != "117.5000" {
		t.Errorf("fx_rate: want %q, got %v", "117.5000", ev["fx_rate"])
	}

	// kafka_key must be deterministic per transaction.
	if _, ok := ev["kafka_key"].(string); !ok {
		t.Errorf("kafka_key field missing from payload")
	}
}

// TestExecuteOrder_FillFailure_DoesNotPublishKafka verifies that when the fill
// handler returns an error, the order row is NOT advanced and no order_filled
// event is published. Regression test for bug #4h.
func TestExecuteOrder_FillFailure_DoesNotPublishKafka(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	failUID := uint64(5555)
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID:                202,
		OwnerType:         model.OwnerClient,
		OwnerID:           &failUID,
		Status:            "approved",
		IsDone:            false,
		Direction:         "buy",
		SecurityType:      "stock",
		Ticker:            "FAIL",
		RemainingPortions: 1,
		Quantity:          1,
		AllOrNone:         true,
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         9,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID:     9,
		Volume: 1_000_000,
		Price:  decimal.NewFromInt(100),
		High:   decimal.NewFromInt(100),
		Low:    decimal.NewFromInt(100),
	}}
	txRepo := &fakeBaseCtxTxRepo{created: make(chan uint64, 1)}
	fillHandler := &failingFillHandler{called: make(chan struct{})}
	pub := &capturingPublisher{}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeBaseCtxSettingRepo{},
		pub, fillHandler,
	)

	engine.StartOrderExecution(context.Background(), 202)

	// Wait for the first fill attempt.
	select {
	case <-fillHandler.called:
	case <-time.After(3 * time.Second):
		t.Fatalf("fill handler never invoked within 3s")
	}

	// Give executeOrder a bit of time to (wrongly) publish, if the bug ever
	// regressed. A successful publish would show up here.
	time.Sleep(150 * time.Millisecond)

	// Cancel to stop the retry loop.
	baseCancel()
	// Allow the goroutine to observe the cancellation.
	time.Sleep(50 * time.Millisecond)

	events := pub.snapshot()
	if len(events) != 0 {
		t.Fatalf("expected 0 kafka events after failed fill, got %d: %+v", len(events), events)
	}

	// Order row must not have been advanced past the failed fill.
	if orderRepo.order.IsDone {
		t.Errorf("order should not be marked done after failed fill")
	}
	if orderRepo.order.RemainingPortions != 1 {
		t.Errorf("remaining_portions should remain 1 after failed fill, got %d", orderRepo.order.RemainingPortions)
	}
}

// TestExecuteOrder_FullFill_EmitsOrderFilledNotification verifies that a fill
// which completes a client-owned order emits an ORDER_FILLED in-app
// notification with quantity/ticker/direction data.
func TestExecuteOrder_FullFill_EmitsOrderFilledNotification(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	uid := uint64(9001)
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID:                301,
		OwnerType:         model.OwnerClient,
		OwnerID:           &uid,
		Status:            "approved",
		IsDone:            false,
		Direction:         "buy",
		SecurityType:      "stock",
		Ticker:            "AAPL",
		RemainingPortions: 5,
		Quantity:          5,
		AllOrNone:         true, // fills all 5 in one portion → order done
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         9,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID:     9,
		Volume: 1_000_000,
		Price:  decimal.NewFromInt(100),
		High:   decimal.NewFromInt(100),
		Low:    decimal.NewFromInt(100),
	}}
	txRepo := &fakeBaseCtxTxRepo{created: make(chan uint64, 1)}
	fillHandler := &crossCurrencyFillHandler{filled: make(chan struct{})}
	pub := &capturingPublisher{}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeBaseCtxSettingRepo{},
		pub, fillHandler,
	)

	engine.StartOrderExecution(context.Background(), 301)

	select {
	case <-fillHandler.filled:
	case <-time.After(3 * time.Second):
		t.Fatalf("fill handler never invoked within 3s")
	}

	deadline := time.Now().Add(3 * time.Second)
	var notifs []contract.GeneralNotificationMessage
	for time.Now().Before(deadline) {
		notifs = pub.notifSnapshot()
		if len(notifs) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected exactly 1 notification after full fill, got %d", len(notifs))
	}

	n := notifs[0]
	if n.Type != "ORDER_FILLED" {
		t.Errorf("Type: want ORDER_FILLED, got %q", n.Type)
	}
	if n.UserID != uid {
		t.Errorf("UserID: want %d, got %d", uid, n.UserID)
	}
	if n.RefType != "order" {
		t.Errorf("RefType: want %q, got %q", "order", n.RefType)
	}
	if n.RefID != 301 {
		t.Errorf("RefID: want 301, got %d", n.RefID)
	}
	if n.Data["quantity"] != "5" {
		t.Errorf("Data[quantity]: want %q, got %q", "5", n.Data["quantity"])
	}
	if n.Data["ticker"] != "AAPL" {
		t.Errorf("Data[ticker]: want %q, got %q", "AAPL", n.Data["ticker"])
	}
	if n.Data["direction"] != "buy" {
		t.Errorf("Data[direction]: want %q, got %q", "buy", n.Data["direction"])
	}
}

// TestExecuteOrder_PartialFill_EmitsPartiallyFilledNotification verifies that a
// fill which does NOT complete a client-owned order emits an
// ORDER_PARTIALLY_FILLED notification carrying the filled quantity.
func TestExecuteOrder_PartialFill_EmitsPartiallyFilledNotification(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	uid := uint64(9002)
	// AllOrNone=false with RemainingPortions=10_000 means the engine's random
	// portion size is in [1, 10_000]; only the single value 10_000 would
	// complete the order, so the first fill is effectively guaranteed partial.
	// Volume is set far above RemainingPortions so calculateWaitTime collapses
	// to ~1s and the fill fires promptly.
	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID:                302,
		OwnerType:         model.OwnerClient,
		OwnerID:           &uid,
		Status:            "approved",
		IsDone:            false,
		Direction:         "sell",
		SecurityType:      "stock",
		Ticker:            "MSFT",
		RemainingPortions: 10_000,
		Quantity:          10_000,
		AllOrNone:         false,
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         9,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID:     9,
		Volume: 100_000_000,
		Price:  decimal.NewFromInt(100),
		High:   decimal.NewFromInt(100),
		Low:    decimal.NewFromInt(100),
	}}
	txRepo := &fakeBaseCtxTxRepo{created: make(chan uint64, 1)}
	fillHandler := &crossCurrencyFillHandler{filled: make(chan struct{})}
	pub := &capturingPublisher{}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeBaseCtxSettingRepo{},
		pub, fillHandler,
	)

	engine.StartOrderExecution(context.Background(), 302)

	select {
	case <-fillHandler.filled:
	case <-time.After(3 * time.Second):
		t.Fatalf("fill handler never invoked within 3s")
	}

	deadline := time.Now().Add(3 * time.Second)
	var notifs []contract.GeneralNotificationMessage
	for time.Now().Before(deadline) {
		notifs = pub.notifSnapshot()
		if len(notifs) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Stop the retry loop so the goroutine doesn't keep firing fills.
	baseCancel()
	if len(notifs) < 1 {
		t.Fatalf("expected >=1 notification after partial fill, got %d", len(notifs))
	}

	n := notifs[0]
	if n.Type != "ORDER_PARTIALLY_FILLED" {
		t.Errorf("Type: want ORDER_PARTIALLY_FILLED, got %q", n.Type)
	}
	if n.UserID != uid {
		t.Errorf("UserID: want %d, got %d", uid, n.UserID)
	}
	if n.RefType != "order" {
		t.Errorf("RefType: want %q, got %q", "order", n.RefType)
	}
	if n.RefID != 302 {
		t.Errorf("RefID: want 302, got %d", n.RefID)
	}
	if n.Data["filled_quantity"] == "" {
		t.Errorf("Data[filled_quantity] should be populated, got empty")
	}
	if n.Data["ticker"] != "MSFT" {
		t.Errorf("Data[ticker]: want %q, got %q", "MSFT", n.Data["ticker"])
	}
	if n.Data["direction"] != "sell" {
		t.Errorf("Data[direction]: want %q, got %q", "sell", n.Data["direction"])
	}
}

// TestExecuteOrder_BankOrderFill_EmitsNoNotification verifies that a fill on a
// bank-owned order produces no in-app notification — there is no end user to
// notify.
func TestExecuteOrder_BankOrderFill_EmitsNoNotification(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	orderRepo := &fakeBaseCtxOrderRepo{order: &model.Order{
		ID:                303,
		OwnerType:         model.OwnerBank,
		OwnerID:           nil,
		Status:            "approved",
		IsDone:            false,
		Direction:         "buy",
		SecurityType:      "stock",
		Ticker:            "AAPL",
		RemainingPortions: 1,
		Quantity:          1,
		AllOrNone:         true,
		OrderType:         "market",
		ContractSize:      1,
		ListingID:         9,
	}}
	listingRepo := &fakeBaseCtxListingRepo{l: &model.Listing{
		ID:     9,
		Volume: 1_000_000,
		Price:  decimal.NewFromInt(100),
		High:   decimal.NewFromInt(100),
		Low:    decimal.NewFromInt(100),
	}}
	txRepo := &fakeBaseCtxTxRepo{created: make(chan uint64, 1)}
	fillHandler := &crossCurrencyFillHandler{filled: make(chan struct{})}
	pub := &capturingPublisher{}

	engine := NewOrderExecutionEngine(
		baseCtx,
		orderRepo, txRepo, listingRepo, &fakeBaseCtxSettingRepo{},
		pub, fillHandler,
	)

	engine.StartOrderExecution(context.Background(), 303)

	select {
	case <-fillHandler.filled:
	case <-time.After(3 * time.Second):
		t.Fatalf("fill handler never invoked within 3s")
	}

	// Give executeOrder time to reach (and skip) the notification step.
	time.Sleep(200 * time.Millisecond)

	if notifs := pub.notifSnapshot(); len(notifs) != 0 {
		t.Fatalf("expected 0 notifications for bank-owned order, got %d: %+v", len(notifs), notifs)
	}
}
