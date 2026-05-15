# Notification Coverage Expansion — Plan A (Foundation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the pipeline that turns notification *intents* into rendered in-app notifications — the `GeneralNotificationMessage.Data` field, a registry-rendering `GeneralNotificationConsumer`, and the 35 `push`-channel template types — so Plan B can simply wire publishers to it.

**Architecture:** Publishers will emit `GeneralNotificationMessage{UserID, Type, Data, ...}` on the existing `notification.general` topic; notification-service's existing `GeneralNotificationConsumer` renders `Type` via Spec 3a's template registry (`push` channel) using `Data`, falling back to the legacy pre-rendered `Title`/`Message` when `Data` is empty. This plan delivers everything *except* the publisher call sites (Plan B).

**Tech Stack:** Go, Kafka (`segmentio/kafka-go`), GORM (notification-service), the 3a template registry/`TemplateService`, golangci-lint, `make` targets.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-coverage-expansion-design.md` (Phases 1–2 of its phasing section).

**Scope note:** This is Plan A of two. Plan B (`2026-05-15-notification-coverage-wiring.md`, written after Plan A executes) does the per-service producer helpers, `EnsureTopics` entries, new + migrated call sites, integration tests, and docs.

---

## File Structure

**Modified:**
- `contract/kafka/messages.go` — `GeneralNotificationMessage` gains a `Data map[string]string` field
- `notification-service/internal/consumer/general_notification_consumer.go` — render via `TemplateService` when `Data` is set
- `notification-service/internal/consumer/general_notification_consumer_test.go` — cover the new render path
- `notification-service/cmd/main.go` — pass `templateSvc` into `NewGeneralNotificationConsumer`
- `notification-service/internal/templates/registry.go` — keep types + `All`/`Get`/`KnownVars`; the combined slice now appends email + push defs
- `notification-service/internal/templates/registry_test.go` — add `TestRegistry_AllPushTypesPresent`

**Created:**
- `notification-service/internal/templates/registry_email.go` — the 13 email `Definition`s moved out of `registry.go` (var `emailDefs`)
- `notification-service/internal/templates/registry_push.go` — the 35 `push` `Definition`s (var `pushDefs`)

---

## Task 1: `GeneralNotificationMessage.Data` field

**Files:**
- Modify: `contract/kafka/messages.go`
- Test: `contract/kafka/messages_test.go` (create if it does not exist)

- [ ] **Step 1: Add the field**

In `contract/kafka/messages.go`, `GeneralNotificationMessage` currently is:

```go
// GeneralNotificationMessage is published by any service that wants to create
// a persistent, user-visible notification (no email, no expiry).
type GeneralNotificationMessage struct {
	UserID  uint64 `json:"user_id"`
	Type    string `json:"type"`               // e.g. "money_received", "loan_approved", "card_issued"
	Title   string `json:"title"`              // human-readable title
	Message string `json:"message"`            // human-readable body
	RefType string `json:"ref_type,omitempty"` // optional: "payment", "transfer", "loan", "card", "account"
	RefID   uint64 `json:"ref_id,omitempty"`   // optional: ID of the referenced entity
}
```

Change it to:

```go
// GeneralNotificationMessage is published by any service that wants to create
// a persistent, user-visible notification (no email, no expiry).
//
// Two rendering modes:
//   - Data non-empty: notification-service renders Type via the template
//     registry (push channel) with Data, producing Title+Message.
//   - Data empty (legacy): Title and Message are used as-is.
type GeneralNotificationMessage struct {
	UserID  uint64            `json:"user_id"`
	Type    string            `json:"type"`               // registry template type, e.g. "ORDER_FILLED"
	Title   string            `json:"title,omitempty"`    // legacy: pre-rendered title
	Message string            `json:"message,omitempty"`  // legacy: pre-rendered body
	Data    map[string]string `json:"data,omitempty"`     // render Type via the registry when set
	RefType string            `json:"ref_type,omitempty"` // optional: "payment", "transfer", "loan", "card", "account"
	RefID   uint64            `json:"ref_id,omitempty"`   // optional: ID of the referenced entity
}
```

- [ ] **Step 2: Write a round-trip test**

Check whether `contract/kafka/messages_test.go` exists. If it does, add the function below to it. If it does not, create `contract/kafka/messages_test.go` with:

```go
package kafka

import (
	"encoding/json"
	"testing"
)

func TestGeneralNotificationMessage_DataRoundTrip(t *testing.T) {
	msg := GeneralNotificationMessage{
		UserID: 42,
		Type:   "ORDER_FILLED",
		Data:   map[string]string{"ticker": "AAPL", "quantity": "10", "direction": "buy"},
		RefType: "order",
		RefID:   7,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got GeneralNotificationMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "ORDER_FILLED" || got.Data["ticker"] != "AAPL" || got.RefID != 7 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Legacy form (no Data) still round-trips.
	legacy := GeneralNotificationMessage{UserID: 1, Type: "password_changed", Title: "T", Message: "M"}
	lb, _ := json.Marshal(legacy)
	var lgot GeneralNotificationMessage
	if err := json.Unmarshal(lb, &lgot); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if lgot.Title != "T" || lgot.Message != "M" || len(lgot.Data) != 0 {
		t.Errorf("legacy round-trip mismatch: %+v", lgot)
	}
}
```

- [ ] **Step 3: Run the test**

Run: `cd contract && go test ./kafka/ -run TestGeneralNotificationMessage_DataRoundTrip`
Expected: PASS.

- [ ] **Step 4: Build the contract module**

Run: `cd contract && go build ./...`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add contract/kafka/messages.go contract/kafka/messages_test.go
git commit -m "feat(contract): GeneralNotificationMessage.Data for templated notifications"
```

---

## Task 2: `GeneralNotificationConsumer` renders via the registry

**Files:**
- Modify: `notification-service/internal/consumer/general_notification_consumer.go`
- Modify: `notification-service/internal/consumer/general_notification_consumer_test.go`
- Modify: `notification-service/cmd/main.go`

The current consumer (verbatim) is:

```go
package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/service"
	kafkago "github.com/segmentio/kafka-go"
)

// generalNotificationCreator is the minimal subset of
// *repository.GeneralNotificationRepository used by GeneralNotificationConsumer.
type generalNotificationCreator interface {
	Create(n *model.GeneralNotification) error
}

type GeneralNotificationConsumer struct {
	reader    *kafkago.Reader
	notifRepo generalNotificationCreator
}

func NewGeneralNotificationConsumer(brokers string, notifRepo *repository.GeneralNotificationRepository) *GeneralNotificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicGeneralNotification,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &GeneralNotificationConsumer{reader: reader, notifRepo: notifRepo}
}

// newGeneralNotificationConsumerForTest constructs a consumer without a Kafka reader.
func newGeneralNotificationConsumerForTest(repo generalNotificationCreator) *GeneralNotificationConsumer {
	return &GeneralNotificationConsumer{notifRepo: repo}
}

func (c *GeneralNotificationConsumer) Start(ctx context.Context) {
	go func() {
		log.Println("general notification consumer started, listening on", kafkamsg.TopicGeneralNotification)
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					log.Println("general notification consumer shutting down")
					return
				}
				log.Printf("general notification consumer: read error: %v", err)
				continue
			}
			c.handleMessage(msg.Value)
		}
	}()
}

func (c *GeneralNotificationConsumer) handleMessage(data []byte) {
	var event kafkamsg.GeneralNotificationMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("general notification consumer: unmarshal error: %v", err)
		return
	}

	notif := &model.GeneralNotification{
		UserID:  event.UserID,
		Type:    event.Type,
		Title:   event.Title,
		Message: event.Message,
		RefType: event.RefType,
		RefID:   event.RefID,
	}
	if err := c.notifRepo.Create(notif); err != nil {
		log.Printf("general notification consumer: create error: %v", err)
		return
	}
	service.NotificationGeneralCreatedTotal.Inc()
}

func (c *GeneralNotificationConsumer) Close() error {
	return c.reader.Close()
}
```

- [ ] **Step 1: Write the failing tests**

The current test file (verbatim) uses a `stubGeneralNotificationCreator` (defined elsewhere in the `consumer` test package — `mocks_test.go`) and a `mustMarshal` helper. Add these tests to `notification-service/internal/consumer/general_notification_consumer_test.go`:

```go
// stubTemplateRenderer implements the renderer interface used by
// GeneralNotificationConsumer. (The consumer package already declares a
// templateRenderer interface — see email_consumer.go.)
type stubGeneralRenderer struct {
	subject, body string
	err           error
	gotType       string
	gotChannel    string
	gotData       map[string]string
}

func (s *stubGeneralRenderer) Render(typ, channel string, data map[string]string) (string, string, error) {
	s.gotType, s.gotChannel, s.gotData = typ, channel, data
	return s.subject, s.body, s.err
}

func TestGeneralNotificationConsumer_HandleMessage_DataRendersViaRegistry(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	rend := &stubGeneralRenderer{subject: "Order filled", body: "Your buy order for 10 AAPL filled."}
	c := newGeneralNotificationConsumerForTest(repo, rend)

	payload := kafkamsg.GeneralNotificationMessage{
		UserID:  42,
		Type:    "ORDER_FILLED",
		Data:    map[string]string{"ticker": "AAPL", "quantity": "10", "direction": "buy"},
		RefType: "order",
		RefID:   7,
	}
	c.handleMessage(mustMarshal(t, payload))

	require.Len(t, repo.created, 1)
	got := repo.created[0]
	assert.Equal(t, "ORDER_FILLED", got.Type)
	assert.Equal(t, "Order filled", got.Title)
	assert.Equal(t, "Your buy order for 10 AAPL filled.", got.Message)
	assert.Equal(t, "order", got.RefType)
	assert.Equal(t, uint64(7), got.RefID)
	// Rendered via the push channel with the message's Data.
	assert.Equal(t, "ORDER_FILLED", rend.gotType)
	assert.Equal(t, "push", rend.gotChannel)
	assert.Equal(t, "AAPL", rend.gotData["ticker"])
}

func TestGeneralNotificationConsumer_HandleMessage_LegacyTitleMessageStillWorks(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	rend := &stubGeneralRenderer{err: errors.New("renderer must not be called for legacy messages")}
	c := newGeneralNotificationConsumerForTest(repo, rend)

	payload := kafkamsg.GeneralNotificationMessage{
		UserID: 1, Type: "password_changed", Title: "Password changed", Message: "Your password was changed.",
	}
	c.handleMessage(mustMarshal(t, payload))

	require.Len(t, repo.created, 1)
	assert.Equal(t, "Password changed", repo.created[0].Title)
	assert.Equal(t, "", rend.gotType, "renderer should not be invoked when Data is empty")
}

func TestGeneralNotificationConsumer_HandleMessage_RenderErrorDropsMessage(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	rend := &stubGeneralRenderer{err: errors.New("unknown type")}
	c := newGeneralNotificationConsumerForTest(repo, rend)

	payload := kafkamsg.GeneralNotificationMessage{
		UserID: 5, Type: "NOPE", Data: map[string]string{"x": "y"},
	}
	require.NotPanics(t, func() { c.handleMessage(mustMarshal(t, payload)) })
	assert.Empty(t, repo.created, "a render error drops the message (nothing stored)")
}
```

The existing `TestGeneralNotificationConsumer_HandleMessage_HappyPath`, `_MalformedPayloadIsIgnored`, and `_RepoError_LogsAndDoesNotPanic` call `newGeneralNotificationConsumerForTest(repo)` with one arg — they must be updated to pass a renderer too: `newGeneralNotificationConsumerForTest(repo, &stubGeneralRenderer{subject: "T", body: "M"})`. (The `_HappyPath` test sends a legacy `Title`/`Message` message, so the renderer is not invoked — the stub's return values are irrelevant there.)

- [ ] **Step 2: Run to confirm failure**

Run: `cd notification-service && go test ./internal/consumer/ -run TestGeneralNotificationConsumer`
Expected: FAIL — `newGeneralNotificationConsumerForTest` takes 1 arg, not 2; `stubGeneralRenderer`'s interface not yet accepted.

- [ ] **Step 3: Implement the consumer change**

Edit `general_notification_consumer.go`. The `templateRenderer` interface is already declared in `email_consumer.go` (same `consumer` package):

```go
// templateRenderer is the minimal subset of *service.TemplateService used here.
type templateRenderer interface {
	Render(typ, channel string, data map[string]string) (subject, body string, err error)
}
```

— reuse it. Make these edits:

Change the struct:

```go
type GeneralNotificationConsumer struct {
	reader    *kafkago.Reader
	notifRepo generalNotificationCreator
	templates templateRenderer
}
```

Change `NewGeneralNotificationConsumer` to accept and store the template service:

```go
func NewGeneralNotificationConsumer(brokers string, notifRepo *repository.GeneralNotificationRepository, templateSvc *service.TemplateService) *GeneralNotificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicGeneralNotification,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &GeneralNotificationConsumer{reader: reader, notifRepo: notifRepo, templates: templateSvc}
}
```

Change `newGeneralNotificationConsumerForTest`:

```go
func newGeneralNotificationConsumerForTest(repo generalNotificationCreator, r templateRenderer) *GeneralNotificationConsumer {
	return &GeneralNotificationConsumer{notifRepo: repo, templates: r}
}
```

Change `handleMessage` — when `Data` is set, render; else legacy:

```go
func (c *GeneralNotificationConsumer) handleMessage(data []byte) {
	var event kafkamsg.GeneralNotificationMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("general notification consumer: unmarshal error: %v", err)
		return
	}

	title, body := event.Title, event.Message
	if len(event.Data) > 0 {
		subject, rendered, err := c.templates.Render(event.Type, "push", event.Data)
		if err != nil {
			log.Printf("general notification consumer: render %q failed, dropping: %v", event.Type, err)
			return
		}
		title, body = subject, rendered
	}

	notif := &model.GeneralNotification{
		UserID:  event.UserID,
		Type:    event.Type,
		Title:   title,
		Message: body,
		RefType: event.RefType,
		RefID:   event.RefID,
	}
	if err := c.notifRepo.Create(notif); err != nil {
		log.Printf("general notification consumer: create error: %v", err)
		return
	}
	service.NotificationGeneralCreatedTotal.Inc()
}
```

The `service` import stays (used for `NewGeneralNotificationConsumer`'s `*service.TemplateService` param and `service.NotificationGeneralCreatedTotal`).

- [ ] **Step 4: Wire `cmd/main.go`**

In `notification-service/cmd/main.go`, the `templateSvc` is already constructed (from the 3a work):

```go
	templateRepo := repository.NewTemplateRepository(db)
	// Template service (registry-backed render + admin CRUD)
	templateSvc := service.NewTemplateService(templateRepo)
```

and the consumer is constructed as:

```go
	generalConsumer := consumer.NewGeneralNotificationConsumer(cfg.KafkaBrokers, notifRepo)
```

Change that line to pass `templateSvc`:

```go
	generalConsumer := consumer.NewGeneralNotificationConsumer(cfg.KafkaBrokers, notifRepo, templateSvc)
```

- [ ] **Step 5: Run the tests + build**

Run: `cd notification-service && go test ./internal/consumer/ && go build ./...`
Expected: all consumer tests PASS; whole service builds clean.

- [ ] **Step 6: Lint**

Run: `cd notification-service && golangci-lint run ./internal/consumer/ ./cmd/`
Expected: no new warnings.

- [ ] **Step 7: Commit**

```bash
git add notification-service/internal/consumer/general_notification_consumer.go notification-service/internal/consumer/general_notification_consumer_test.go notification-service/cmd/main.go
git commit -m "feat(notification-service): render GeneralNotification via the template registry when Data is set"
```

---

## Task 3: Split the registry by channel

**Files:**
- Modify: `notification-service/internal/templates/registry.go`
- Create: `notification-service/internal/templates/registry_email.go`

This is a pure refactor — behavior identical, 3a's `registry_test.go` must still pass unchanged.

- [ ] **Step 1: Move the email definitions into `registry_email.go`**

`registry.go` currently holds `package templates`, the `Variable` and `Definition` types, `var registry = []Definition{ ...13 email entries... }`, and the `All`/`Get`/`KnownVars` functions.

Create `notification-service/internal/templates/registry_email.go` with:

```go
package templates

// emailDefs are the code-defined email template types. Their default
// subject/body were migrated from the pre-3a sender.BuildEmail switch.
var emailDefs = []Definition{
	// ... move the 13 email Definition{...} literals here verbatim ...
}
```

Cut the **13 email `Definition{...}` literals** out of `registry.go`'s `var registry = []Definition{...}` and paste them as the elements of `emailDefs` in `registry_email.go`. Do not change any field content.

- [ ] **Step 2: Update `registry.go` to assemble the combined slice**

In `registry.go`, replace `var registry = []Definition{ ...13 entries... }` with:

```go
// registry is the full set of known template types, assembled from the
// per-channel definition slices in registry_email.go and registry_push.go.
var registry = func() []Definition {
	all := make([]Definition, 0, len(emailDefs)+len(pushDefs))
	all = append(all, emailDefs...)
	all = append(all, pushDefs...)
	return all
}()
```

The `Variable`/`Definition` type declarations and the `All`/`Get`/`KnownVars` functions stay in `registry.go` unchanged. `pushDefs` is defined in Task 4 — until then `registry.go` will not compile; that is expected (Tasks 3 and 4 commit together at the end of Task 4).

- [ ] **Step 3: (deferred)** Build + test happen at the end of Task 4, since `pushDefs` does not exist yet.

---

## Task 4: The 35 `push` template types

**Files:**
- Create: `notification-service/internal/templates/registry_push.go`
- Modify: `notification-service/internal/templates/registry_test.go`

- [ ] **Step 1: Write `registry_push.go`**

Create `notification-service/internal/templates/registry_push.go` with EXACTLY this content. (Push templates are short — a title in `DefaultSubject`, a one-line body in `DefaultBody`. Every `{{var}}` must appear in the entry's `Variables` list — 3a's `TestRegistry_SelfConsistent` enforces this.)

```go
package templates

// pushDefs are the code-defined in-app ("push" channel) notification template
// types. Publishers emit GeneralNotificationMessage{Type, Data} and
// notification-service renders the Type via this registry.
var pushDefs = []Definition{
	// ── Orders ───────────────────────────────────────────────────────────────
	{
		Type: "ORDER_PLACED", Channel: "push",
		Description: "An order was placed.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
			{"order_type", "Order type", "market"},
		},
		DefaultSubject: "Order placed",
		DefaultBody:    "Your {{order_type}} order to {{direction}} {{quantity}} {{ticker}} has been placed.",
	},
	{
		Type: "ORDER_APPROVED", Channel: "push",
		Description: "A pending order was approved.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order approved",
		DefaultBody:    "Your order to {{direction}} {{quantity}} {{ticker}} has been approved.",
	},
	{
		Type: "ORDER_DECLINED", Channel: "push",
		Description: "A pending order was declined.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order declined",
		DefaultBody:    "Your order to {{direction}} {{quantity}} {{ticker}} was declined.",
	},
	{
		Type: "ORDER_PARTIALLY_FILLED", Channel: "push",
		Description: "An order was partially filled.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"filled_quantity", "Units filled in this fill", "3"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order partially filled",
		DefaultBody:    "Your {{direction}} order for {{ticker}} partially filled — {{filled_quantity}} units.",
	},
	{
		Type: "ORDER_FILLED", Channel: "push",
		Description: "An order was fully filled.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order filled",
		DefaultBody:    "Your {{direction}} order for {{quantity}} {{ticker}} has been fully filled.",
	},
	{
		Type: "ORDER_CANCELLED", Channel: "push",
		Description: "An order was cancelled.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order cancelled",
		DefaultBody:    "Your order to {{direction}} {{quantity}} {{ticker}} has been cancelled.",
	},
	// ── OTC ──────────────────────────────────────────────────────────────────
	{
		Type: "OTC_OFFER_RECEIVED", Channel: "push",
		Description: "An OTC option offer was sent to you.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"quantity", "Contract quantity", "100"},
			{"strike_price", "Strike price", "150.00"},
			{"premium", "Option premium", "5.00"},
		},
		DefaultSubject: "New OTC offer",
		DefaultBody:    "You received an OTC offer on {{ticker}} — {{quantity}} @ strike {{strike_price}}, premium {{premium}}.",
	},
	{
		Type: "OTC_OFFER_COUNTERED", Channel: "push",
		Description: "The other party countered your OTC offer.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"quantity", "Contract quantity", "100"},
			{"strike_price", "Strike price", "150.00"},
			{"premium", "Option premium", "5.00"},
		},
		DefaultSubject: "OTC offer countered",
		DefaultBody:    "Your OTC offer on {{ticker}} was countered — now {{quantity}} @ strike {{strike_price}}, premium {{premium}}.",
	},
	{
		Type: "OTC_OFFER_REJECTED", Channel: "push",
		Description: "The other party rejected your OTC offer.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC offer rejected",
		DefaultBody:    "Your OTC offer on {{ticker}} was rejected.",
	},
	{
		Type: "OTC_OFFER_EXPIRED", Channel: "push",
		Description: "An OTC offer expired without being accepted.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC offer expired",
		DefaultBody:    "Your OTC offer on {{ticker}} has expired.",
	},
	{
		Type: "OTC_CONTRACT_CREATED", Channel: "push",
		Description: "An OTC offer was accepted and a contract was created.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"quantity", "Contract quantity", "100"},
			{"strike_price", "Strike price", "150.00"},
			{"premium_paid", "Premium paid", "5.00"},
		},
		DefaultSubject: "OTC contract created",
		DefaultBody:    "An OTC offer was accepted — contract on {{ticker}}, {{quantity}} @ strike {{strike_price}}, premium {{premium_paid}}.",
	},
	{
		Type: "OTC_CONTRACT_EXERCISED", Channel: "push",
		Description: "An OTC option contract was exercised.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"shares_transferred", "Shares transferred", "100"},
			{"strike_amount_paid", "Strike amount paid", "15000.00"},
		},
		DefaultSubject: "OTC contract exercised",
		DefaultBody:    "An OTC contract on {{ticker}} was exercised — {{shares_transferred}} shares for {{strike_amount_paid}}.",
	},
	{
		Type: "OTC_CONTRACT_EXPIRED", Channel: "push",
		Description: "An OTC option contract expired unexercised.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC contract expired",
		DefaultBody:    "Your OTC contract on {{ticker}} has expired.",
	},
	{
		Type: "OTC_CONTRACT_FAILED", Channel: "push",
		Description: "An OTC option contract failed during settlement.",
		Variables: []Variable{
			{"failure_reason", "Why the contract failed", "insufficient funds"},
		},
		DefaultSubject: "OTC contract failed",
		DefaultBody:    "An OTC contract could not be completed: {{failure_reason}}.",
	},
	// ── Money movement ───────────────────────────────────────────────────────
	{
		Type: "PAYMENT_SENT", Channel: "push",
		Description: "A payment was sent from your account.",
		Variables: []Variable{
			{"amount", "Payment amount", "1000.00 RSD"},
			{"to_account", "Destination account number", "265-2-22"},
		},
		DefaultSubject: "Payment sent",
		DefaultBody:    "{{amount}} was sent from your account to {{to_account}}.",
	},
	{
		Type: "PAYMENT_RECEIVED", Channel: "push",
		Description: "A payment was credited to your account.",
		Variables: []Variable{
			{"amount", "Payment amount", "1000.00 RSD"},
			{"from_account", "Source account number", "265-1-11"},
		},
		DefaultSubject: "Payment received",
		DefaultBody:    "{{amount}} was credited to your account from {{from_account}}.",
	},
	{
		Type: "PAYMENT_FAILED", Channel: "push",
		Description: "A payment failed.",
		Variables: []Variable{
			{"amount", "Payment amount", "1000.00 RSD"},
			{"failure_reason", "Why the payment failed", "insufficient funds"},
		},
		DefaultSubject: "Payment failed",
		DefaultBody:    "Your payment of {{amount}} failed: {{failure_reason}}.",
	},
	{
		Type: "TRANSFER_SENT", Channel: "push",
		Description: "A transfer was sent from your account.",
		Variables: []Variable{
			{"amount", "Transfer amount", "1000.00 RSD"},
			{"to_account", "Destination account number", "265-2-22"},
		},
		DefaultSubject: "Transfer sent",
		DefaultBody:    "{{amount}} was transferred from your account to {{to_account}}.",
	},
	{
		Type: "TRANSFER_RECEIVED", Channel: "push",
		Description: "A transfer was credited to your account.",
		Variables: []Variable{
			{"final_amount", "Amount credited", "1000.00 RSD"},
			{"from_account", "Source account number", "265-1-11"},
		},
		DefaultSubject: "Transfer received",
		DefaultBody:    "{{final_amount}} was credited to your account from {{from_account}}.",
	},
	{
		Type: "TRANSFER_FAILED", Channel: "push",
		Description: "A transfer failed.",
		Variables: []Variable{
			{"amount", "Transfer amount", "1000.00 RSD"},
			{"failure_reason", "Why the transfer failed", "insufficient funds"},
		},
		DefaultSubject: "Transfer failed",
		DefaultBody:    "Your transfer of {{amount}} failed: {{failure_reason}}.",
	},
	// ── Loans ────────────────────────────────────────────────────────────────
	{
		Type: "LOAN_REQUEST_SUBMITTED", Channel: "push",
		Description: "A loan request was submitted.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Requested amount", "500000.00"},
		},
		DefaultSubject: "Loan request submitted",
		DefaultBody:    "Your {{loan_type}} loan request for {{amount}} has been submitted.",
	},
	{
		Type: "LOAN_REQUEST_APPROVED", Channel: "push",
		Description: "A loan request was approved.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Approved amount", "500000.00"},
		},
		DefaultSubject: "Loan approved",
		DefaultBody:    "Your {{loan_type}} loan request for {{amount}} has been approved.",
	},
	{
		Type: "LOAN_REQUEST_REJECTED", Channel: "push",
		Description: "A loan request was rejected.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Requested amount", "500000.00"},
		},
		DefaultSubject: "Loan request rejected",
		DefaultBody:    "Your {{loan_type}} loan request for {{amount}} was not approved.",
	},
	{
		Type: "LOAN_DISBURSED", Channel: "push",
		Description: "Loan funds were disbursed.",
		Variables: []Variable{
			{"loan_number", "Loan number", "LN-000123"},
			{"amount", "Disbursed amount", "500000.00"},
			{"currency", "Currency code", "RSD"},
		},
		DefaultSubject: "Loan disbursed",
		DefaultBody:    "Loan {{loan_number}} has been disbursed — {{amount}} {{currency}}.",
	},
	{
		Type: "INSTALLMENT_COLLECTED", Channel: "push",
		Description: "A loan installment was collected.",
		Variables: []Variable{
			{"amount", "Installment amount", "12500.00"},
		},
		DefaultSubject: "Installment paid",
		DefaultBody:    "A loan installment of {{amount}} has been collected.",
	},
	{
		Type: "INSTALLMENT_FAILED", Channel: "push",
		Description: "A loan installment collection failed.",
		Variables: []Variable{
			{"amount", "Installment amount", "12500.00"},
			{"retry_deadline", "Retry deadline", "2026-06-01"},
		},
		DefaultSubject: "Installment payment failed",
		DefaultBody:    "We couldn't collect a loan installment of {{amount}}. Please ensure funds are available by {{retry_deadline}}.",
	},
	// ── Accounts ─────────────────────────────────────────────────────────────
	{
		Type: "ACCOUNT_OPENED", Channel: "push",
		Description: "A new account was opened.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"currency", "Currency code", "RSD"},
		},
		DefaultSubject: "Account opened",
		DefaultBody:    "Your new {{currency}} account {{account_number}} has been opened.",
	},
	{
		Type: "ACCOUNT_STATUS_CHANGED", Channel: "push",
		Description: "An account's status changed.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"new_status", "New account status", "inactive"},
		},
		DefaultSubject: "Account status changed",
		DefaultBody:    "The status of account {{account_number}} changed to {{new_status}}.",
	},
	{
		Type: "ACCOUNT_NAME_UPDATED", Channel: "push",
		Description: "An account's name was updated.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"new_name", "New account name", "Savings"},
		},
		DefaultSubject: "Account name updated",
		DefaultBody:    "Account {{account_number}} was renamed to \"{{new_name}}\".",
	},
	{
		Type: "ACCOUNT_LIMITS_UPDATED", Channel: "push",
		Description: "An account's spending limits were updated.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"daily_limit", "New daily limit", "100000.00"},
			{"monthly_limit", "New monthly limit", "1000000.00"},
		},
		DefaultSubject: "Account limits updated",
		DefaultBody:    "Limits for account {{account_number}} updated — daily {{daily_limit}}, monthly {{monthly_limit}}.",
	},
	{
		Type: "MAINTENANCE_FEE_CHARGED", Channel: "push",
		Description: "A maintenance fee was charged.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"amount", "Fee amount", "300.00"},
			{"currency", "Currency code", "RSD"},
		},
		DefaultSubject: "Maintenance fee charged",
		DefaultBody:    "A maintenance fee of {{amount}} {{currency}} was charged to account {{account_number}}.",
	},
	// ── Cards ────────────────────────────────────────────────────────────────
	{
		Type: "CARD_CREATED", Channel: "push",
		Description: "A new card was issued.",
		Variables: []Variable{
			{"card_brand", "Card brand", "visa"},
		},
		DefaultSubject: "Card issued",
		DefaultBody:    "A new {{card_brand}} card has been issued for your account.",
	},
	{
		Type: "CARD_STATUS_CHANGED", Channel: "push",
		Description: "A card's status changed.",
		Variables: []Variable{
			{"new_status", "New card status", "blocked"},
		},
		DefaultSubject: "Card status changed",
		DefaultBody:    "Your card status changed to {{new_status}}.",
	},
	{
		Type: "CARD_TEMPORARY_BLOCKED", Channel: "push",
		Description: "A card was temporarily blocked.",
		Variables: []Variable{
			{"expires_at", "When the block lifts", "2026-06-01"},
			{"reason", "Block reason", "customer request"},
		},
		DefaultSubject: "Card temporarily blocked",
		DefaultBody:    "Your card has been temporarily blocked ({{reason}}) until {{expires_at}}.",
	},
	{
		Type: "VIRTUAL_CARD_CREATED", Channel: "push",
		Description: "A virtual card was created.",
		Variables: []Variable{
			{"usage_type", "Virtual card usage type", "single_use"},
		},
		DefaultSubject: "Virtual card created",
		DefaultBody:    "A {{usage_type}} virtual card has been created for your account.",
	},
	{
		Type: "CARD_REQUEST_CREATED", Channel: "push",
		Description: "A card request was submitted.",
		Variables: []Variable{
			{"card_brand", "Requested card brand", "visa"},
		},
		DefaultSubject: "Card request submitted",
		DefaultBody:    "Your request for a {{card_brand}} card has been submitted.",
	},
	{
		Type: "CARD_REQUEST_APPROVED", Channel: "push",
		Description: "A card request was approved.",
		Variables: []Variable{
			{"card_brand", "Requested card brand", "visa"},
		},
		DefaultSubject: "Card request approved",
		DefaultBody:    "Your {{card_brand}} card request has been approved.",
	},
	{
		Type: "CARD_REQUEST_REJECTED", Channel: "push",
		Description: "A card request was rejected.",
		Variables: []Variable{
			{"reason", "Rejection reason", "incomplete documentation"},
		},
		DefaultSubject: "Card request rejected",
		DefaultBody:    "Your card request was rejected: {{reason}}.",
	},
}
```

- [ ] **Step 2: Add the push-registry test**

In `notification-service/internal/templates/registry_test.go`, add:

```go
func TestRegistry_AllPushTypesPresent(t *testing.T) {
	want := []string{
		"ORDER_PLACED", "ORDER_APPROVED", "ORDER_DECLINED", "ORDER_PARTIALLY_FILLED",
		"ORDER_FILLED", "ORDER_CANCELLED",
		"OTC_OFFER_RECEIVED", "OTC_OFFER_COUNTERED", "OTC_OFFER_REJECTED", "OTC_OFFER_EXPIRED",
		"OTC_CONTRACT_CREATED", "OTC_CONTRACT_EXERCISED", "OTC_CONTRACT_EXPIRED", "OTC_CONTRACT_FAILED",
		"PAYMENT_SENT", "PAYMENT_RECEIVED", "PAYMENT_FAILED",
		"TRANSFER_SENT", "TRANSFER_RECEIVED", "TRANSFER_FAILED",
		"LOAN_REQUEST_SUBMITTED", "LOAN_REQUEST_APPROVED", "LOAN_REQUEST_REJECTED",
		"LOAN_DISBURSED", "INSTALLMENT_COLLECTED", "INSTALLMENT_FAILED",
		"ACCOUNT_OPENED", "ACCOUNT_STATUS_CHANGED", "ACCOUNT_NAME_UPDATED",
		"ACCOUNT_LIMITS_UPDATED", "MAINTENANCE_FEE_CHARGED",
		"CARD_CREATED", "CARD_STATUS_CHANGED", "CARD_TEMPORARY_BLOCKED",
		"VIRTUAL_CARD_CREATED", "CARD_REQUEST_CREATED", "CARD_REQUEST_APPROVED", "CARD_REQUEST_REJECTED",
	}
	for _, typ := range want {
		if _, ok := Get(typ, "push"); !ok {
			t.Errorf("registry missing push type %q", typ)
		}
	}
	if got := len(All("push")); got != len(want) {
		t.Errorf("All(push) returned %d, want %d", got, len(want))
	}
}
```

(The `want` slice has 38 entries — the spec named 35; the count here is the authoritative list. If `All("push")` and `len(want)` disagree, the registry and this test must be reconciled — the test is the spec of record for Plan B's `Type` constants.)

- [ ] **Step 3: Build + run the templates package tests**

Run: `cd notification-service && go build ./internal/templates/ && go test ./internal/templates/`
Expected: builds clean; `TestRegistry_AllEmailTypesPresent`, `TestRegistry_SelfConsistent` (3a, unchanged), and `TestRegistry_AllPushTypesPresent` all PASS. `TestRegistry_SelfConsistent` validates that every `{{var}}` in every push entry's default text is a declared variable — if it fails, fix the offending entry.

- [ ] **Step 4: Build + test the whole notification-service**

Run: `cd notification-service && go build ./... && go test ./... && golangci-lint run ./...`
Expected: builds, all tests pass, no new lint warnings.

- [ ] **Step 5: Commit Tasks 3 + 4 together**

```bash
git add notification-service/internal/templates/
git commit -m "feat(notification-service): split registry by channel + 38 push template types"
```

---

## Final verification

- [ ] **Step 1: Full build + test + lint**

Run: `make build`
Expected: all services compile (the `contract` change is additive — every service still builds).

Run: `cd contract && go test ./... && cd ../notification-service && go test ./...`
Expected: all pass.

Run: `cd notification-service && golangci-lint run ./...`
Expected: clean.

---

## Self-Review Notes

- **Spec coverage:** Plan A covers spec Phases 1 (contract `Data` + consumer render path → Tasks 1, 2) and 2 (registry split + push types → Tasks 3, 4). Spec Phases 3–9 (publisher call sites, integration, docs) are Plan B.
- **Placeholder scan:** Task 3 says "move the 13 email `Definition{...}` literals verbatim" rather than re-pasting ~280 lines that already exist in `registry.go` from the 3a work — this is a *move* of existing committed code, not new content, so the literal text lives in the repo already; the instruction is exact about what moves where. Everything else is concrete.
- **Type consistency:** `templateRenderer` interface (reused from `email_consumer.go`) signature `Render(typ, channel string, data map[string]string) (subject, body string, err error)` matches `stubGeneralRenderer` (Task 2) and `*service.TemplateService` (the 3a type). `Definition`/`Variable` field names in `registry_push.go` (Task 4) match the 3a `registry.go` types used by Task 3's `emailDefs`. `NewGeneralNotificationConsumer(brokers, notifRepo, templateSvc)` (Task 2) matches the `cmd/main.go` call (Task 2 Step 4).
- **Count note:** the push registry has **38** entries, not the spec's "35" — the spec's prose under-counted its own tables (orders 6 + OTC 8 + money 6 + loans 6 + accounts 5 + cards 7 = 38). `TestRegistry_AllPushTypesPresent` enumerates all 38; that list is authoritative for Plan B.
- **Build order:** Task 3 leaves `registry.go` referencing `pushDefs` before it exists — Tasks 3 and 4 are committed together (Task 4 Step 5); Task 3 has no standalone build/test step for this reason.
