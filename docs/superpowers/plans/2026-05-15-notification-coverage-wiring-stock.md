# Notification Coverage — Plan B1 (stock-service wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire stock-service to emit in-app notification intents for every significant order and OTC action, rendered via the template registry built in Plan A.

**Architecture:** At each point where stock-service already publishes a domain event (`stock.order-*`, `otc.*`), it additionally emits a `kafkamsg.GeneralNotificationMessage{UserID, Type, Data, RefType, RefID}` on `notification.general` — best-effort, after the action commits, only for **client-owned** orders/parties (bank-owned → no notification). notification-service's `GeneralNotificationConsumer` (Plan A) renders these via the registry `push` channel.

**Tech Stack:** Go, Kafka, gRPC/protobuf (`contract/stockpb`), GORM (stock-service), golangci-lint, `make` targets.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-coverage-expansion-design.md`. This is Plan B1 of 5 per-service wiring plans (B2 transaction, B3 credit, B4 account, B5 card).

**Prerequisite:** Plan A is done — `kafkamsg.GeneralNotificationMessage` has a `Data map[string]string` field; the registry has `push`-channel types `ORDER_PLACED`, `ORDER_APPROVED`, `ORDER_DECLINED`, `ORDER_PARTIALLY_FILLED`, `ORDER_FILLED`, `ORDER_CANCELLED`, `OTC_OFFER_RECEIVED`, `OTC_OFFER_COUNTERED`, `OTC_OFFER_REJECTED`, `OTC_OFFER_EXPIRED`, `OTC_CONTRACT_CREATED`, `OTC_CONTRACT_EXERCISED`, `OTC_CONTRACT_EXPIRED`, `OTC_CONTRACT_FAILED`.

---

## File Structure

**Modified:**
- `stock-service/internal/kafka/producer.go` — add `PublishGeneralNotification`
- `stock-service/cmd/main.go` — add `notification.general` to `EnsureTopics`
- `stock-service/internal/service/order_service.go` — extend `OrderEventPublisher` interface; emit at 4 order sites; a `notifyOrder` helper
- `stock-service/internal/service/order_service_test.go` — extend `mockProducer`; assert notification emits
- `stock-service/internal/service/order_execution.go` — extend `OrderFilledPublisher` interface; emit at the fill site
- `stock-service/internal/service/order_execution_test.go` — extend the fill-publisher mock; assert
- `contract/proto/stock/stock.proto` — `CreateOTCOfferRequest` gains a `ticker` field
- `stock-service/internal/model/otc_offer.go` — `OTCOffer` gains a `Ticker` column
- `stock-service/internal/model/option_contract.go` — `OptionContract` gains a `Ticker` column
- `stock-service/internal/handler/otc_options_handler.go` — pass `ticker` into the create-offer service input
- `stock-service/internal/service/otc_offer_service.go` — `CreateOfferInput.Ticker`; persist it; copy to revisions where relevant; emit at offer-created / countered / rejected
- `stock-service/internal/service/otc_accept_saga.go` — copy `Ticker` to the contract; emit at contract-created
- `stock-service/internal/service/otc_exercise_saga.go` — emit at contract-exercised
- `stock-service/internal/service/otc_expiry_cron.go` — emit at offer-expired / contract-expired
- `api-gateway/internal/handler/otc_options_handler.go` — pass the already-resolved `ticker` into `CreateOTCOfferRequest`
- `docs/Specification.md` — note the new emits (brief)

---

## Phase 1 — Orders

### Task 1: Producer method + EnsureTopics + interface

**Files:**
- Modify: `stock-service/internal/kafka/producer.go`
- Modify: `stock-service/cmd/main.go`
- Modify: `stock-service/internal/service/order_service.go`

- [ ] **Step 1: Add `PublishGeneralNotification` to the producer**

`stock-service/internal/kafka/producer.go` has a `Producer` wrapping `*shared.Producer` with a generic `Publish(ctx, topic, msg any) error`. After the existing `PublishOrderCancelled` method, add:

```go
func (p *Producer) PublishGeneralNotification(ctx context.Context, msg contract.GeneralNotificationMessage) error {
	return p.inner.Publish(ctx, contract.TopicGeneralNotification, msg)
}
```

(`contract` is the existing import alias for `github.com/exbanka/contract/kafka` in this file.)

- [ ] **Step 2: Add `notification.general` to `EnsureTopics`**

In `stock-service/cmd/main.go`, the `shared.EnsureTopics(cfg.KafkaBrokers, ...)` call lists stock + otc topics. Add `"notification.general"` as the final entry in that list (before the closing `)`).

- [ ] **Step 3: Extend the `OrderEventPublisher` interface**

In `order_service.go`, find the `OrderEventPublisher` interface (the type of `OrderService.producer`). It currently declares `PublishOrderCreated`/`PublishOrderApproved`/`PublishOrderDeclined`/`PublishOrderCancelled` (all `(ctx context.Context, msg interface{}) error`). Add one method:

```go
	PublishGeneralNotification(ctx context.Context, msg contract.GeneralNotificationMessage) error
```

Use whatever alias `order_service.go` already imports `github.com/exbanka/contract/kafka` under (check the import block — it may be `contract` or `kafkamsg`; match it). If `contract/kafka` is not yet imported in this file, add it.

- [ ] **Step 4: Build stock-service**

Run: `cd stock-service && go build ./...`
Expected: builds clean (the real `*kafka.Producer` now satisfies the extended interface; nothing calls the new method yet).

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/kafka/producer.go stock-service/cmd/main.go stock-service/internal/service/order_service.go
git commit -m "feat(stock-service): producer.PublishGeneralNotification + EnsureTopics notification.general"
```

### Task 2: `notifyOrder` helper + the 4 order-service emit sites

**Files:**
- Modify: `stock-service/internal/service/order_service.go`
- Modify: `stock-service/internal/service/order_service_test.go`

The 4 existing publish sites in `order_service.go` (verbatim):
- created — pending path:
  ```go
	if needsApproval {
		// Order sits in "pending" until a supervisor calls ApproveOrder.
		// Publish the order-created event with status=pending.
		if s.producer != nil {
			evt := buildOrderEvent(order)
			go func() { _ = s.producer.PublishOrderCreated(context.Background(), evt) }()
		}
		return order, nil
	}
  ```
- created — auto-approved path:
  ```go
	if s.producer != nil {
		evt := buildOrderEvent(order)
		go func() { _ = s.producer.PublishOrderCreated(context.Background(), evt) }()
	}
  ```
- approved: `if s.producer != nil { go func() { _ = s.producer.PublishOrderApproved(context.Background(), buildOrderEvent(order)) }() }`
- declined: `if s.producer != nil { go func() { _ = s.producer.PublishOrderDeclined(context.Background(), buildOrderEvent(order)) }() }`
- cancelled: `if s.producer != nil { go func() { _ = s.producer.PublishOrderCancelled(context.Background(), buildOrderEvent(order)) }() }`

`buildOrderEvent` (verbatim) shows the `order` fields in scope: `order.ID`, `order.OwnerType`, `order.OwnerID`, `order.Direction`, `order.OrderType`, `order.SecurityType`, `order.Ticker`, `order.Quantity`, `order.Status`.

- [ ] **Step 1: Write the failing test**

In `order_service_test.go`, the `mockProducer` currently has `created/approved/declined/cancelled int` counters. Add a notifications slice and the method. Change the struct to:

```go
type mockProducer struct {
	created   int
	approved  int
	declined  int
	cancelled int
	notifs    []contract.GeneralNotificationMessage
}
```

(import `contract "github.com/exbanka/contract/kafka"` in the test file if not already there — match the alias used in `order_service.go`.) Add the method:

```go
func (m *mockProducer) PublishGeneralNotification(_ context.Context, msg contract.GeneralNotificationMessage) error {
	m.notifs = append(m.notifs, msg)
	return nil
}
```

Then add a test that a client-owned order placement emits an `ORDER_PLACED` notification, and a bank-owned one does not. Follow the existing order-placement test setup in the file for constructing the service + a client-owned vs bank-owned order:

```go
func TestOrderService_PlaceOrder_EmitsClientNotification(t *testing.T) {
	// Build the service exactly as the neighbouring placement tests do, with
	// a *mockProducer. Place a CLIENT-owned market order that auto-approves.
	// (Copy the construction block from the nearest existing
	// TestOrderService_PlaceOrder_* test.)
	fx, prod := newOrderServiceForTest(t) // match the existing helper/name
	_, err := fx.PlaceOrder(context.Background(), /* client-owned order input */)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	// Allow the go func() publish to run.
	time.Sleep(50 * time.Millisecond)
	var placed *contract.GeneralNotificationMessage
	for i := range prod.notifs {
		if prod.notifs[i].Type == "ORDER_PLACED" {
			placed = &prod.notifs[i]
		}
	}
	if placed == nil {
		t.Fatal("expected an ORDER_PLACED notification")
	}
	if placed.UserID == 0 || placed.Data["ticker"] == "" || placed.RefType != "order" {
		t.Errorf("bad notification: %+v", placed)
	}
}

func TestOrderService_PlaceOrder_BankOwned_NoNotification(t *testing.T) {
	// Same, but a BANK-owned order (OwnerType="bank", OwnerID=nil).
	fx, prod := newOrderServiceForTest(t)
	_, err := fx.PlaceOrder(context.Background(), /* bank-owned order input */)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	for _, n := range prod.notifs {
		if n.Type == "ORDER_PLACED" {
			t.Errorf("bank-owned order must not emit a notification, got %+v", n)
		}
	}
}
```

If the existing tests don't expose a `(service, *mockProducer)` helper, copy the inline construction block from the nearest `TestOrderService_PlaceOrder_*` test verbatim and capture the `*mockProducer` reference.

- [ ] **Step 2: Run to confirm failure**

Run: `cd stock-service && go test ./internal/service/ -run 'TestOrderService_PlaceOrder_EmitsClientNotification|TestOrderService_PlaceOrder_BankOwned_NoNotification'`
Expected: FAIL — `mockProducer` has no `PublishGeneralNotification` until Step 1 compiles, then the notification assertion fails because nothing emits yet.

- [ ] **Step 3: Add the `notifyOrder` helper**

In `order_service.go`, add this helper near `buildOrderEvent`:

```go
// notifyOrder emits an in-app notification for an order action. It is a no-op
// for bank-owned orders (no human recipient) and best-effort (failures are
// swallowed — Kafka is observability, not a correctness gate). Mirrors the
// existing best-effort `go func` publish pattern in this file.
func (s *OrderService) notifyOrder(order *model.Order, notifType string, data map[string]string) {
	if s.producer == nil || order.OwnerType != model.OwnerClient || order.OwnerID == nil {
		return
	}
	msg := contract.GeneralNotificationMessage{
		UserID:  *order.OwnerID,
		Type:    notifType,
		Data:    data,
		RefType: "order",
		RefID:   order.ID,
	}
	go func() { _ = s.producer.PublishGeneralNotification(context.Background(), msg) }()
}
```

Confirm `model.OwnerClient` is the correct constant name for the client owner type (the OTC code uses `model.OwnerClient` — match it). Confirm `order.Quantity`'s type so the `data` map below formats it correctly (it is an `int64` per `buildOrderEvent` — use `strconv.FormatInt`; add `strconv` to imports if needed).

- [ ] **Step 4: Add the emit calls at the 4 sites**

Right after each existing `PublishOrderX` `go func()` line, add the matching `notifyOrder` call. The `order` variable is in scope at every site.

After the **pending-path** `PublishOrderCreated`:
```go
	s.notifyOrder(order, "ORDER_PLACED", map[string]string{
		"direction": order.Direction, "quantity": strconv.FormatInt(order.Quantity, 10),
		"ticker": order.Ticker, "order_type": order.OrderType,
	})
```

After the **auto-approved-path** `PublishOrderCreated` — same `ORDER_PLACED` block as above.

After `PublishOrderApproved`:
```go
	s.notifyOrder(order, "ORDER_APPROVED", map[string]string{
		"direction": order.Direction, "quantity": strconv.FormatInt(order.Quantity, 10), "ticker": order.Ticker,
	})
```

After `PublishOrderDeclined`:
```go
	s.notifyOrder(order, "ORDER_DECLINED", map[string]string{
		"direction": order.Direction, "quantity": strconv.FormatInt(order.Quantity, 10), "ticker": order.Ticker,
	})
```

After `PublishOrderCancelled`:
```go
	s.notifyOrder(order, "ORDER_CANCELLED", map[string]string{
		"direction": order.Direction, "quantity": strconv.FormatInt(order.Quantity, 10), "ticker": order.Ticker,
	})
```

- [ ] **Step 5: Run the tests**

Run: `cd stock-service && go test ./internal/service/ -run TestOrderService_PlaceOrder`
Expected: PASS (the new tests + all existing order-placement tests).

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/order_service.go stock-service/internal/service/order_service_test.go
git commit -m "feat(stock-service): emit in-app notifications for order placed/approved/declined/cancelled"
```

### Task 3: Order-fill emit (partial + full)

**Files:**
- Modify: `stock-service/internal/service/order_execution.go`
- Modify: `stock-service/internal/service/order_execution_test.go`

The fill site (verbatim, in `order_execution.go`) publishes `e.producer.PublishOrderFilled(e.baseCtx, payload)` where `payload` is a `map[string]any` and the in-scope variables are `order` (with `OwnerType`, `OwnerID`, `Direction`, `Ticker`, `ID`, `IsDone`), `txn`, `portionSize` (the units filled in this fill), `e.baseCtx`. The engine struct is `OrderExecutionEngine` with a `producer OrderFilledPublisher` field.

- [ ] **Step 1: Extend the `OrderFilledPublisher` interface**

In `order_execution.go`, find the `OrderFilledPublisher` interface (the type of `OrderExecutionEngine.producer`). Add:

```go
	PublishGeneralNotification(ctx context.Context, msg contract.GeneralNotificationMessage) error
```

(match the `contract/kafka` import alias used in this file; add the import if missing.)

- [ ] **Step 2: Write the failing test**

In `order_execution_test.go`, find the mock implementing `OrderFilledPublisher`. Add a `notifs []contract.GeneralNotificationMessage` field and:

```go
func (m *<mockName>) PublishGeneralNotification(_ context.Context, msg contract.GeneralNotificationMessage) error {
	m.notifs = append(m.notifs, msg)
	return nil
}
```

(replace `<mockName>` with the actual mock type in that file). Add a test that a client-owned order fill emits a notification — `ORDER_PARTIALLY_FILLED` when `order.IsDone == false`, `ORDER_FILLED` when `order.IsDone == true` — and a bank-owned fill emits nothing. Follow the existing fill-engine test setup in the file.

- [ ] **Step 3: Run to confirm failure**

Run: `cd stock-service && go test ./internal/service/ -run <fill test name>`
Expected: FAIL.

- [ ] **Step 4: Implement the emit**

In `order_execution.go`, immediately after the existing `if err := e.producer.PublishOrderFilled(e.baseCtx, payload); err != nil { ... }` block, add:

```go
		if e.producer != nil && order.OwnerType == model.OwnerClient && order.OwnerID != nil {
			notifType := "ORDER_PARTIALLY_FILLED"
			data := map[string]string{
				"direction":       order.Direction,
				"filled_quantity": strconv.FormatInt(portionSize, 10),
				"ticker":          order.Ticker,
			}
			if order.IsDone {
				notifType = "ORDER_FILLED"
				data = map[string]string{
					"direction": order.Direction,
					"quantity":  strconv.FormatInt(order.Quantity, 10),
					"ticker":    order.Ticker,
				}
			}
			_ = e.producer.PublishGeneralNotification(e.baseCtx, contract.GeneralNotificationMessage{
				UserID:  *order.OwnerID,
				Type:    notifType,
				Data:    data,
				RefType: "order",
				RefID:   order.ID,
			})
		}
```

Confirm `portionSize` and `order.Quantity` types (both `int64` per the verbatim payload) and that `strconv` is imported (add if needed).

- [ ] **Step 5: Run the tests**

Run: `cd stock-service && go test ./internal/service/ -run <fill test name>`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/order_execution.go stock-service/internal/service/order_execution_test.go
git commit -m "feat(stock-service): emit in-app notifications for order partial + full fills"
```

---

## Phase 2 — OTC ticker plumbing

### Task 4: Carry `ticker` on OTC offers and contracts

**Files:**
- Modify: `contract/proto/stock/stock.proto` (+regen `contract/stockpb`)
- Modify: `stock-service/internal/model/otc_offer.go`
- Modify: `stock-service/internal/model/option_contract.go`
- Modify: `stock-service/internal/service/otc_offer_service.go`
- Modify: `stock-service/internal/handler/otc_options_handler.go`
- Modify: `stock-service/internal/service/otc_accept_saga.go`
- Modify: `api-gateway/internal/handler/otc_options_handler.go`

OTC notification text needs a human ticker, but OTC offers/contracts only store `StockID`. The api-gateway's `CreateOffer` handler already has the client-supplied `ticker` (Spec 1 resolves it to a stock_id) — so thread the ticker through and persist it on the offer; the contract copies it at accept.

- [ ] **Step 1: Add `ticker` to `CreateOTCOfferRequest`**

In `contract/proto/stock/stock.proto`, `CreateOTCOfferRequest` currently ends with the fields added in Spec 1 (`account_id`, `on_behalf_of_client_id` at field numbers 10–11). Add:

```proto
  string ticker = 12;
```

Run: `make proto` — expect `contract/stockpb` regenerated.

- [ ] **Step 2: api-gateway passes the ticker through**

In `api-gateway/internal/handler/otc_options_handler.go`, `CreateOffer` resolves the ticker to a stock via `h.security.GetStockByTicker` then builds `&stockpb.CreateOTCOfferRequest{...}`. Add `Ticker: req.Ticker` to that struct literal (`req.Ticker` is the client-supplied ticker, already in scope from the request body).

- [ ] **Step 3: `OTCOffer.Ticker` column**

In `stock-service/internal/model/otc_offer.go`, add to the `OTCOffer` struct (next to `StockID`):

```go
	Ticker string `gorm:"size:16;not null;default:''" json:"ticker"`
```

- [ ] **Step 4: `OptionContract.Ticker` column**

In `stock-service/internal/model/option_contract.go`, add to the `OptionContract` struct (next to `StockID`):

```go
	Ticker string `gorm:"size:16;not null;default:''" json:"ticker"`
```

- [ ] **Step 5: `CreateOfferInput.Ticker` + persist it**

In `stock-service/internal/service/otc_offer_service.go`, the `CreateOfferInput` struct (defined in Spec 1 with `InitiatorAccountID`) gains:

```go
	Ticker string
```

In `Create`, the `&model.OTCOffer{...}` literal sets `InitiatorAccountID: in.InitiatorAccountID` — add `Ticker: in.Ticker` alongside it.

- [ ] **Step 6: stock-service OTC handler passes the ticker**

In `stock-service/internal/handler/otc_options_handler.go`, `CreateOffer` builds `service.CreateOfferInput{...}` — add `Ticker: in.Ticker` (the proto request field from Step 1).

- [ ] **Step 7: Copy `Ticker` onto the contract at accept**

In `stock-service/internal/service/otc_accept_saga.go`, the `Accept` method builds `contract := &model.OptionContract{...}` from the offer `o`. Add `Ticker: o.Ticker` to that literal.

- [ ] **Step 8: Build + regenerate-affected modules**

Run: `cd contract && go build ./... && cd ../stock-service && go build ./... && cd ../api-gateway && go build ./...`
Expected: all build clean.

- [ ] **Step 9: Migration test**

Add to the existing OTC model test file (`stock-service/internal/model/otc_coverage_test.go` or wherever `OTCOffer` round-trip tests live) a check that `OTCOffer.Ticker` and `OptionContract.Ticker` persist and read back:

```go
func TestOTCOffer_Ticker_Persists(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.OTCOffer{})
	id := uint64(1)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &id,
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "AAPL",
		Quantity: decimal.NewFromInt(1), StrikePrice: decimal.NewFromInt(1), Premium: decimal.NewFromInt(1),
		SettlementDate: time.Now().Add(48 * time.Hour), Status: model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 1,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got model.OTCOffer
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Ticker != "AAPL" {
		t.Errorf("got ticker %q, want AAPL", got.Ticker)
	}
}
```

Run: `cd stock-service && go test ./internal/model/ -run TestOTCOffer_Ticker_Persists` — expect PASS.

- [ ] **Step 10: Update Spec 1's OTC handler tests + integration tests**

The Spec 1 unit tests for the stock-service OTC handler and the api-gateway OTC handler build `CreateOTCOfferRequest` / `createOTCOfferRequest` literals — they still compile (the new `Ticker` field is optional in Go struct literals). Run `cd stock-service && go test ./internal/handler/ && cd ../api-gateway && go test ./internal/handler/` to confirm nothing broke. If the api-gateway `CreateOffer` test asserts on the exact `CreateOTCOfferRequest` passed to the stub, update it to also expect `Ticker`.

- [ ] **Step 11: Commit**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/ stock-service/internal/model/otc_offer.go stock-service/internal/model/option_contract.go stock-service/internal/service/otc_offer_service.go stock-service/internal/handler/otc_options_handler.go stock-service/internal/service/otc_accept_saga.go api-gateway/internal/handler/otc_options_handler.go stock-service/internal/model/
git commit -m "feat(stock-service): carry ticker on OTC offers + contracts for notification rendering"
```

---

## Phase 3 — OTC emits

### Task 5: `notifyOTCParty` helper + offer-created / countered / rejected emits

**Files:**
- Modify: `stock-service/internal/service/otc_offer_service.go`
- Modify: `stock-service/internal/service/otc_offer_service_crud_test.go` (or the OTC service test file)

`OTCOfferService` has a `producer *kafkaprod.Producer` field (which now has `PublishGeneralNotification` from Task 1 — `kafkaprod` is the import alias). The `OTCParty` type is `{OwnerType string, OwnerID *uint64, BankCode string}`. The three publish sites in `otc_offer_service.go` are offer-created (`payload.Initiator`, `payload.Counterparty`), offer-countered (`payload.ModifiedBy`, `payload.OtherParty`), offer-rejected (`payload.RejectedBy`, `payload.OtherParty`). The offer object `o` has `o.Ticker` (Task 4), `o.ID`, `o.Quantity`, `o.StrikePrice`, `o.Premium` (all `decimal.Decimal` — use `.String()`).

- [ ] **Step 1: Add the `notifyOTCParty` helper**

In `otc_offer_service.go`, add:

```go
// notifyOTCParty emits an in-app notification to one OTC party. It is a no-op
// for bank parties (OwnerType != "client" or nil OwnerID) and best-effort.
func (s *OTCOfferService) notifyOTCParty(ctx context.Context, party kafkamsg.OTCParty, notifType string, offerID uint64, data map[string]string) {
	if s.producer == nil || party.OwnerType != "client" || party.OwnerID == nil {
		return
	}
	_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  *party.OwnerID,
		Type:    notifType,
		Data:    data,
		RefType: "otc_offer",
		RefID:   offerID,
	})
}
```

(match the `contract/kafka` import alias used in `otc_offer_service.go` — the file already imports it as `kafkamsg` for the `OTCOfferCreatedMessage` etc.; use that alias.)

- [ ] **Step 2: Emit at offer-created**

In `Create`, after the existing `if data, err := json.Marshal(payload); err == nil { s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferCreated, data, "") }` block (still inside the `if s.producer != nil {` block), add — notify the named counterparty if there is one (`OTC_OFFER_RECEIVED`):

```go
		if o.CounterpartyOwnerType != nil {
			s.notifyOTCParty(ctx, kafkamsg.OTCParty{
				OwnerType: string(*o.CounterpartyOwnerType), OwnerID: o.CounterpartyOwnerID,
			}, "OTC_OFFER_RECEIVED", o.ID, map[string]string{
				"ticker": o.Ticker, "quantity": o.Quantity.String(),
				"strike_price": o.StrikePrice.String(), "premium": o.Premium.String(),
			})
		}
```

(Confirm the `OTCOffer` field names for the counterparty owner — `CounterpartyOwnerType *OwnerType`, `CounterpartyOwnerID *uint64` per the model. A broadcast offer has nil counterparty → no notification, correct.)

- [ ] **Step 3: Emit at offer-countered**

In `Counter`, after the `publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferCountered, ...)` line, the `otcOtherParty(o, in.ActorUserID, in.ActorSystemType)` helper already computes the non-acting party. Add:

```go
		s.notifyOTCParty(ctx, otcOtherParty(o, in.ActorUserID, in.ActorSystemType), "OTC_OFFER_COUNTERED", o.ID, map[string]string{
			"ticker": o.Ticker, "quantity": o.Quantity.String(),
			"strike_price": o.StrikePrice.String(), "premium": o.Premium.String(),
		})
```

- [ ] **Step 4: Emit at offer-rejected**

In `Reject`, after the `publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferRejected, ...)` line:

```go
		s.notifyOTCParty(ctx, otcOtherParty(o, in.ActorUserID, in.ActorSystemType), "OTC_OFFER_REJECTED", o.ID, map[string]string{
			"ticker": o.Ticker,
		})
```

- [ ] **Step 5: Write the tests**

In the OTC service test file, add tests asserting: a `Create` with a named **client** counterparty emits one `OTC_OFFER_RECEIVED` to that counterparty; a `Create` with a **bank** counterparty (or broadcast) emits none; a `Counter` emits one `OTC_OFFER_COUNTERED` to the non-acting party; a `Reject` emits one `OTC_OFFER_REJECTED`. The OTC test fixture uses a real `*kafkaprod.Producer`? — check: if the fixture passes `nil` for the producer, the `notifyOTCParty` no-ops. To assert emits, the test fixture must use a producer mock. If the existing OTC test fixture builds the service with `nil` producer, add a minimal stub producer that records `PublishGeneralNotification` calls and inject it via the fixture. Match the existing fixture construction (`newOTCCRUDFixture` from Spec 1's work) — extend it to accept/expose a recording producer.

Example shape:

```go
type recordingOTCProducer struct {
	notifs []kafkamsg.GeneralNotificationMessage
}
func (r *recordingOTCProducer) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.notifs = append(r.notifs, m)
	return nil
}
```

— but note `OTCOfferService.producer` is the concrete `*kafkaprod.Producer`, not an interface. If it cannot be stubbed without an interface, the minimal change is: introduce a narrow `otcNotifier` interface (`PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage) error`) as the type of a NEW `notifier` field on `OTCOfferService`, wired to the same `*kafkaprod.Producer` in the constructor, and have `notifyOTCParty` use `s.notifier`. If you take this route, report it as a DONE_WITH_CONCERNS note. Otherwise (if `producer` is already an interface or easily stubbed) keep it simple.

Run: `cd stock-service && go test ./internal/service/ -run 'OTC.*Notif|Notif.*OTC'` (adjust to your test names) — expect PASS.

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/otc_offer_service.go stock-service/internal/service/
git commit -m "feat(stock-service): emit in-app notifications for OTC offer received/countered/rejected"
```

### Task 6: contract-created / exercised / expired + offer-expired emits

**Files:**
- Modify: `stock-service/internal/service/otc_accept_saga.go`
- Modify: `stock-service/internal/service/otc_exercise_saga.go`
- Modify: `stock-service/internal/service/otc_expiry_cron.go`
- Test: the corresponding `*_test.go` files

These three files all build OTC event payloads with `Buyer`/`Seller` `OTCParty` (or `Initiator`/`Counterparty`) and have access to the same `s.producer` / `cr.producer` `*kafkaprod.Producer` (or the `notifier` interface from Task 5 if that route was taken — use the same mechanism). The `notifyOTCParty` helper from Task 5 lives on `*OTCOfferService` — `otc_accept_saga.go` and `otc_exercise_saga.go` are methods on `*OTCOfferService` so they can call it directly; `otc_expiry_cron.go` is a separate `*OTCExpiryCron` type — give it its own equivalent helper or a free function.

- [ ] **Step 1: Emit at contract-created (accept saga)**

In `otc_accept_saga.go`, after the `publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractCreated, data, sagaID)` line, the `buyerOwnerType/buyerOwnerID` and `sellerOwnerType/sellerOwnerID` are in scope, and `contract` has `Ticker` (Task 4), `Quantity`, `StrikePrice`, `PremiumPaid` (decimals). Add — notify both client parties:

```go
		notifData := map[string]string{
			"ticker": contract.Ticker, "quantity": contract.Quantity.String(),
			"strike_price": contract.StrikePrice.String(), "premium_paid": contract.PremiumPaid.String(),
		}
		s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(buyerOwnerType), OwnerID: buyerOwnerID}, "OTC_CONTRACT_CREATED", o.ID, notifData)
		s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(sellerOwnerType), OwnerID: sellerOwnerID}, "OTC_CONTRACT_CREATED", o.ID, notifData)
```

Note `notifyOTCParty`'s `RefType` is hardcoded `"otc_offer"` — for contract events that is acceptable (the offer id links the negotiation thread); if you prefer `"otc_contract"`/contract id, add a `refType, refID` parameter to `notifyOTCParty` and thread it through Task 5's three calls too. Pick one and be consistent; the simplest is to add the two params to the helper. **If you add the params, update the Task 5 call sites accordingly.**

- [ ] **Step 2: Emit at contract-exercised (exercise saga)**

In `otc_exercise_saga.go`, after `publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCContractExercised, data, sagaID)`, `c` (the contract) has `Ticker`, `BuyerOwnerType/ID`, `SellerOwnerType/ID`; `strikeSellerCcy` and `qty` are in scope. Add:

```go
		exData := map[string]string{
			"ticker": c.Ticker, "shares_transferred": decimal.NewFromInt(qty).String(),
			"strike_amount_paid": strikeSellerCcy.String(),
		}
		s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID}, "OTC_CONTRACT_EXERCISED", c.OfferID, exData)
		s.notifyOTCParty(ctx, kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID}, "OTC_CONTRACT_EXERCISED", c.OfferID, exData)
```

(`c.OfferID` links back to the offer; if you switched `notifyOTCParty` to take refType/refID, pass `"otc_contract", c.ID`.)

- [ ] **Step 3: Emit at offer-expired + contract-expired (expiry cron)**

In `otc_expiry_cron.go`, the `*OTCExpiryCron` type has `cr.producer`. Add a local helper method on `*OTCExpiryCron` mirroring `notifyOTCParty` (same body, using `cr.producer`), or a package-level free function `notifyOTCPartyVia(producer, ctx, party, type, refID, data)` shared by both types — your call; keep it DRY.

After the offer-expired `publishSagaEvent(...)`: notify the initiator and (if present) the counterparty with `OTC_OFFER_EXPIRED`, data `{"ticker": o.Ticker}`.
After the contract-expired `publishSagaEvent(...)`: notify the buyer and seller with `OTC_CONTRACT_EXPIRED`, data `{"ticker": c.Ticker}`.

(`o`/`c` are in scope at each site per the verbatim cron code; `o.Ticker`/`c.Ticker` exist after Task 4.)

- [ ] **Step 4: `OTC_CONTRACT_FAILED`**

The `OTCContractFailedMessage` has no party info and the spec lists `OTC_CONTRACT_FAILED` as buyer+seller. Find where `otc.contract-failed` is published (search `TopicOTCContractFailed` in stock-service). If the publishing site has the contract's buyer/seller owners in scope, emit `OTC_CONTRACT_FAILED` to both with data `{"failure_reason": <reason>}`. If the buyer/seller owners are **not** reachable at that site without extra plumbing, skip `OTC_CONTRACT_FAILED` and note it as a DONE_WITH_CONCERNS item — it is the one OTC event whose publish site may lack recipient context, and adding it is not worth a saga-shape change in this plan.

- [ ] **Step 5: Tests**

Add tests to the accept-saga, exercise-saga, and expiry-cron test files asserting the expected `OTC_CONTRACT_CREATED` (×2), `OTC_CONTRACT_EXERCISED` (×2), `OTC_OFFER_EXPIRED`, `OTC_CONTRACT_EXPIRED` notifications are emitted to the client parties (and not to bank parties). Reuse the recording-producer mechanism from Task 5.

Run: `cd stock-service && go test ./internal/service/` — expect all PASS.

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/otc_accept_saga.go stock-service/internal/service/otc_exercise_saga.go stock-service/internal/service/otc_expiry_cron.go stock-service/internal/service/
git commit -m "feat(stock-service): emit in-app notifications for OTC contract created/exercised/expired + offer expired"
```

---

## Phase 4 — Verification + docs

### Task 7: Full build/test/lint + Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Update `docs/Specification.md`**

In the Kafka/events section, add a short note: stock-service now emits `GeneralNotificationMessage` intents on `notification.general` for order placed/approved/declined/cancelled, order partial+full fills, and OTC offer received/countered/rejected/expired + contract created/exercised/expired — client-owned only, best-effort, rendered by notification-service via the `push`-channel registry. Note the new `OTCOffer.Ticker` / `OptionContract.Ticker` columns and the `CreateOTCOfferRequest.ticker` field in the relevant §18/§17 entries.

- [ ] **Step 2: Full build + test + lint**

Run: `make build`
Expected: all services compile.

Run: `cd contract && go test ./... && cd ../stock-service && go test ./... && cd ../api-gateway && go test ./...`
Expected: all pass.

Run: `cd stock-service && golangci-lint run ./... && cd ../api-gateway && golangci-lint run ./... && cd ../contract && golangci-lint run ./...`
Expected: no new warnings.

- [ ] **Step 3: Commit**

```bash
git add docs/Specification.md
git commit -m "docs: stock-service in-app notification emits + OTC ticker columns"
```

---

## Self-Review Notes

- **Spec coverage:** order events (placed/approved/declined/cancelled/partial-fill/fill) → Tasks 2–3; OTC events (offer received/countered/rejected/expired, contract created/exercised/expired/failed) → Tasks 5–6; the spec's recipient rules (client-owned only, bank parties skipped, two-party emits multiple) → `notifyOrder` (Task 2) + `notifyOTCParty` (Task 5); the OTC-ticker decision ("add ticker to the payloads/models") → Task 4. `EnsureTopics` + producer helper → Task 1.
- **Placeholder scan:** Task 2/3's test steps say "copy the construction block from the nearest existing test" rather than re-pasting test-fixture boilerplate this plan can't see verbatim — that is an exact instruction (which block, what to capture), not a vague placeholder. Task 5 Step 5 and Task 6 Step 4 flag two genuine unknowns (whether `OTCOfferService.producer` is stubbable without an interface; whether the `contract-failed` publish site has recipient context) with explicit decision rules and a DONE_WITH_CONCERNS escape — these are real codebase facts the implementer must check, not hand-waving.
- **Type consistency:** `PublishGeneralNotification(ctx, contract.GeneralNotificationMessage) error` is the signature on the producer (Task 1), the `OrderEventPublisher` interface (Task 1), the `OrderFilledPublisher` interface (Task 3), and `kafkaprod.Producer` (used by OTC, Task 5). `notifyOrder(order, type, data)` (Task 2) and `notifyOTCParty(ctx, party, type, offerID, data)` (Task 5) — note Task 6 Step 1 explicitly flags the optional refType/refID parameter addition and says to update Task 5's call sites if taken. `OTCOffer.Ticker` / `OptionContract.Ticker` / `CreateOfferInput.Ticker` / `CreateOTCOfferRequest.ticker` (Task 4) are used in Tasks 5–6.
- **Build order:** Task 1 ends compiling (the new interface method is satisfied by the real producer; no caller yet). Task 4's proto + model changes are additive — every Spec 1 OTC test still compiles (new struct fields are optional in literals). Tasks are independently committable.
