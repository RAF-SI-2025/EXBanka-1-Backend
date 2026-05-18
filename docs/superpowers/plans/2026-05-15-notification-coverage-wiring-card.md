# Notification Coverage — Plan B5 (card-service wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire card-service to emit in-app notification intents for every card and card-request lifecycle event — migrating 2 existing hardcoded `Title/Message` emits to the `Data` form and adding 6 new emit sites.

**Architecture:** card-service already has `producer.PublishGeneralNotification` and `notification.general` in `EnsureTopics`. Today it emits 2 `GeneralNotificationMessage`s with hardcoded Title/Message from the gRPC handler (`card_issued` in `CreateCard`, `card_blocked` in `BlockCard`) via the `producerFacade` interface. New emits needed: `CARD_STATUS_CHANGED` on Unblock/Deactivate (in addition to migrating BlockCard's existing emit to `CARD_STATUS_CHANGED`), `VIRTUAL_CARD_CREATED` on CreateVirtualCard, `CARD_TEMPORARY_BLOCKED` (service layer), `CARD_REQUEST_CREATED/APPROVED/REJECTED` (service layer).

**Tech Stack:** Go, Kafka, GORM, golangci-lint.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-coverage-expansion-design.md` §2 Cards table, §3 recipient rules. Plan B5 of 5 (B1 stock + B2 transaction + B3 credit + B4 account shipped).

**Prerequisite:** Plan A done — `GeneralNotificationMessage` has `Data`; registry has `push` types `CARD_CREATED`, `CARD_STATUS_CHANGED`, `CARD_TEMPORARY_BLOCKED`, `VIRTUAL_CARD_CREATED`, `CARD_REQUEST_CREATED`, `CARD_REQUEST_APPROVED`, `CARD_REQUEST_REJECTED`.

**Recipient rule:** Recipient is the card/request owner client. For `Card`, the model has `OwnerID uint64` and `OwnerType string` (`"client"` or `"authorized_person"`). The owner-resolution decision: **only emit when `card.OwnerType == "client"`** — authorized_person cards belong to a business account's authorized person, not a direct client login; resolving the linked client is out of scope for B5. For `CardRequest`, the `ClientID` field is always the client (no owner type).

**Target Type strings + Data keys:**
| Type | Recipient | Data keys | RefType / RefID |
|---|---|---|---|
| `CARD_CREATED` | client owner | `card_brand` | `card` / card.ID |
| `CARD_STATUS_CHANGED` | client owner | `new_status` | `card` / card.ID |
| `CARD_TEMPORARY_BLOCKED` | client owner | `expires_at, reason` | `card` / card.ID |
| `VIRTUAL_CARD_CREATED` | client owner | `usage_type` | `card` / card.ID |
| `CARD_REQUEST_CREATED` | request owner (client) | `card_brand` | `card_request` / request.ID |
| `CARD_REQUEST_APPROVED` | request owner | `card_brand` | `card_request` / request.ID |
| `CARD_REQUEST_REJECTED` | request owner | `reason` | `card_request` / request.ID |

Best-effort `_ =` discard.

---

## File Structure

**Modified:**
- `card-service/internal/handler/grpc_handler.go` — migrate `card_issued` → `CARD_CREATED`; migrate `card_blocked` → `CARD_STATUS_CHANGED`; add `CARD_STATUS_CHANGED` for Unblock + Deactivate; add `VIRTUAL_CARD_CREATED` for CreateVirtualCard.
- `card-service/internal/handler/grpc_handler_test.go` — extend `stubProducer` to capture full message payloads (currently only counts); add per-emit tests.
- `card-service/internal/handler/interfaces.go` — `producerFacade` interface is fine as-is (`PublishGeneralNotification` already listed).
- `card-service/internal/service/card_service.go` — add `CARD_TEMPORARY_BLOCKED` emit in `TemporaryBlockCard`.
- `card-service/internal/service/card_request_service.go` — add `CARD_REQUEST_CREATED/APPROVED/REJECTED` emits in `CreateRequest`/`ApproveRequest`/`RejectRequest`.
- Tests for the service-layer emits (need narrow notifier interface — same precedent as B2/B3/B4 cron tasks).
- `docs/Specification.md` — note the new/migrated emits.

---

## Task 1: Extend `stubProducer` to capture messages + migrate handler emits + add status-changed/virtual

**Files:**
- Modify: `card-service/internal/handler/helpers_test.go` (where `stubProducer` is defined)
- Modify: `card-service/internal/handler/grpc_handler.go`
- Modify: `card-service/internal/handler/grpc_handler_test.go`

The current `stubProducer` in `helpers_test.go:107-133` only tracks `generalNotificationCount int` — not the message contents. We need to capture messages to assert Type/Data/RefType/RefID.

Existing handler emits in `grpc_handler.go`:
- `CreateCard` (~lines 51-58): `Type: "card_issued"`, hardcoded Title/Message → migrate to `Type: "CARD_CREATED"`, `Data: {"card_brand": card.CardBrand}`.
- `BlockCard` (~lines 118-125): `Type: "card_blocked"`, hardcoded Title/Message → migrate to `Type: "CARD_STATUS_CHANGED"`, `Data: {"new_status": card.Status}` (which is `"blocked"` after the action).

New handler emits to add:
- `UnblockCard` (around line 161-165 in service or where handler responds): emit `CARD_STATUS_CHANGED` with `Data: {"new_status": "active"}`.
- `DeactivateCard` (around line 201-205): emit `CARD_STATUS_CHANGED` with `Data: {"new_status": "deactivated"}`.
- `CreateVirtualCard`: emit `VIRTUAL_CARD_CREATED` with `Data: {"usage_type": card.UsageType}`.

Recipient: `card.OwnerID` ONLY when `card.OwnerType == "client"` (skip authorized_person cards).

- [ ] **Step 1: Extend `stubProducer` to capture messages**

In `card-service/internal/handler/helpers_test.go`, modify `stubProducer`:

```go
type stubProducer struct {
	cardCreatedCount         int
	cardStatusChangedCount   int
	sendEmailCount           int
	generalNotificationCount int
	failSendEmail            bool

	cardCreatedMsgs         []kafkamsg.CardCreatedMessage
	cardStatusChangedMsgs   []kafkamsg.CardStatusChangedMessage
	generalNotifications    []kafkamsg.GeneralNotificationMessage
}
```

Update each method to append the message to the corresponding slice while keeping the count counters (some existing tests may assert on counts — preserve those). Specifically:

```go
func (p *stubProducer) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	p.generalNotificationCount++
	p.generalNotifications = append(p.generalNotifications, m)
	return nil
}
```

Same idea for `PublishCardCreated` and `PublishCardStatusChanged`.

- [ ] **Step 2: Write failing tests**

In `grpc_handler_test.go`, add tests using the existing fixture pattern (whatever `newTestHandler()`-equivalent is used). For each, build a CLIENT-owned card setup (`card.OwnerType = "client"`).

1. **`TestCreateCard_EmitsCardCreatedNotification`** — call `CreateCard`; assert `len(prod.generalNotifications)` includes one `CARD_CREATED` with `UserID == card.OwnerID`, `Data["card_brand"] == card.CardBrand`, `RefType == "card"`, `RefID == card.ID`, empty Title/Message.

2. **`TestCreateCard_AuthorizedPerson_NoNotification`** — card with `OwnerType == "authorized_person"`; assert no `CARD_CREATED` captured. Domain event (`PublishCardCreated`) still fires.

3. **`TestBlockCard_EmitsStatusChangedNotification`** — `Type == "CARD_STATUS_CHANGED"`, `Data["new_status"] == "blocked"`.

4. **`TestUnblockCard_EmitsStatusChangedNotification`** — `Data["new_status"] == "active"`.

5. **`TestDeactivateCard_EmitsStatusChangedNotification`** — `Data["new_status"] == "deactivated"`.

6. **`TestCreateVirtualCard_EmitsVirtualCardCreatedNotification`** — `Type == "VIRTUAL_CARD_CREATED"`, `Data["usage_type"] == card.UsageType` (e.g. `"single_use"`).

Run: expect FAIL.

- [ ] **Step 3: Migrate the existing emits + add new ones in `grpc_handler.go`**

**`CreateCard`** — replace the existing emit (~lines 51-58) with:
```go
	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_CREATED",
			Data:    map[string]string{"card_brand": card.CardBrand},
			RefType: "card",
			RefID:   card.ID,
		})
	}
```

**`BlockCard`** — replace the existing emit (~lines 118-125) with:
```go
	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_STATUS_CHANGED",
			Data:    map[string]string{"new_status": card.Status},
			RefType: "card",
			RefID:   card.ID,
		})
	}
```

**`UnblockCard`** — find the handler (likely around the existing `PublishCardStatusChanged` at lines 161-165). After that publish, add the same `CARD_STATUS_CHANGED` block.

**`DeactivateCard`** — same: add `CARD_STATUS_CHANGED` after the existing `PublishCardStatusChanged` (lines 201-205).

**`CreateVirtualCard`** — find the handler method (the explore mentioned `CreateVirtualCard` exists in card_service.go at line 280; find the handler that calls it). After the existing domain publish (likely `PublishVirtualCardCreated`), add:
```go
	if card.OwnerType == "client" {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "VIRTUAL_CARD_CREATED",
			Data:    map[string]string{"usage_type": card.UsageType},
			RefType: "card",
			RefID:   card.ID,
		})
	}
```

Drop legacy Title/Message fields entirely from the migrated emits. Best-effort `_ =` discard. The `producerFacade` interface already supports `PublishGeneralNotification` — no interface change needed.

If `fmt.Sprintf` was used only for the dropped Title/Message strings, check the file's other uses and remove the `fmt` import if completely unused.

Read the actual code first — variable names, where exactly in each handler the existing domain publish sits, what `card` variable is available, etc.

- [ ] **Step 4: Run tests + lint**

```
cd card-service && go test ./internal/handler/ -count=1
cd card-service && golangci-lint run ./internal/handler/
```
Expected: PASS / clean.

- [ ] **Step 5: Commit**

```bash
git add card-service/internal/handler/grpc_handler.go card-service/internal/handler/grpc_handler_test.go card-service/internal/handler/helpers_test.go
git commit -m "feat(card-service): emit CARD_CREATED/STATUS_CHANGED/VIRTUAL_CARD_CREATED in-app notifications via Data form"
```

## Task 2: CARD_TEMPORARY_BLOCKED service-layer emit

**Files:**
- Modify: `card-service/internal/service/card_service.go`
- Modify or create: `card-service/internal/service/card_service_test.go`

`TemporaryBlockCard` (card_service.go ~lines 350-386) is in the SERVICE layer. It already calls `s.producer.PublishCardTemporaryBlocked` at the end. The service's `producer` field is the concrete `*kafka.Producer` (or a small interface — check). For testability, introduce a narrow notifier interface — same precedent as B1/B2/B3/B4.

- [ ] **Step 1: Read the current code**

Read `card_service.go` fully — find the `CardService` struct, the `NewCardService` constructor, the `producer` field type (concrete or interface), and the `TemporaryBlockCard` method (~lines 350-386). After the existing `PublishCardTemporaryBlocked` call, `card` (with `OwnerID`, `OwnerType`), `expiresAt`, and `reason` are in scope.

Run `ls card-service/internal/service/card_service_test.go` to see if a test file exists.

- [ ] **Step 2: Introduce a notifier interface (if needed)**

If `s.producer` is the concrete `*kafka.Producer` and there's no existing way to inject a stub, introduce:

```go
type cardNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}
```

Add `notifier cardNotifier` field to `CardService`. In `NewCardService`, wire `s.notifier = producer` with the typed-nil guard pattern (`if producer != nil { s.notifier = producer }`). If the existing `producer` field is already an interface that supports `PublishGeneralNotification`, you can use `s.producer` directly and skip the new field.

- [ ] **Step 3: Write failing test**

Add **`TestCardService_TemporaryBlockCard_EmitsNotification`** asserting one captured notification with `Type == "CARD_TEMPORARY_BLOCKED"`, `UserID == card.OwnerID`, `Data["expires_at"] != ""`, `Data["reason"] == reason`, `RefType == "card"`, `RefID == card.ID`. Plus **`TestCardService_TemporaryBlockCard_AuthorizedPerson_NoNotification`**.

Use a recording notifier stub and inject via `svc.notifier = rec` after construction (or via the existing producer interface if applicable). If the cron-style helper-extraction pattern from B3/B4 fits better (small `notifyTemporaryBlock` helper testable directly), use that.

Run: expect FAIL.

- [ ] **Step 4: Implement**

After the existing `_ = s.producer.PublishCardTemporaryBlocked(...)` (or wherever the domain publish sits), add:

```go
	if card.OwnerType == "client" {
		_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  card.OwnerID,
			Type:    "CARD_TEMPORARY_BLOCKED",
			Data:    map[string]string{"expires_at": expiresAt.Format(time.RFC3339), "reason": reason},
			RefType: "card",
			RefID:   card.ID,
		})
	}
```

If you introduced `s.notifier`, use it instead. Best-effort. Match the actual variable names.

- [ ] **Step 5: Run tests + lint + commit**

```
cd card-service && go test ./... -count=1
cd card-service && golangci-lint run ./internal/service/
git add card-service/internal/service/card_service.go card-service/internal/service/card_service_test.go
git commit -m "feat(card-service): emit CARD_TEMPORARY_BLOCKED in-app notification from service layer"
```

## Task 3: CARD_REQUEST_CREATED / APPROVED / REJECTED emits

**Files:**
- Modify: `card-service/internal/service/card_request_service.go`
- Modify or create: `card-service/internal/service/card_request_service_test.go`

`CardRequestService` (card_request_service.go) has three methods with existing domain Kafka publishes:
- `CreateRequest` (~lines 23-42): `PublishCardRequestCreated`. `req.ClientID` is the recipient. `Data: {"card_brand": req.CardBrand}`.
- `ApproveRequest` (~lines 56-82): `PublishCardRequestApproved`. Recipient: the request's `ClientID` (re-load if needed). `Data: {"card_brand": req.CardBrand}`.
- `RejectRequest` (~lines 84-101): `PublishCardRequestRejected`. Recipient: `req.ClientID`. `Data: {"reason": reason}`.

`CardRequest.ClientID` is always a client (no owner_type field — design says always client).

The service's `producer` field — same analysis as Task 2. If concrete, introduce `cardRequestNotifier` interface (or reuse `cardNotifier` from Task 2 if same package). Same typed-nil pattern.

- [ ] **Step 1: Read the current code**

Read `card_request_service.go` fully. Confirm the variable names available at each emit site (`req`, `id`, `card`, `employeeID`, `reason`).

- [ ] **Step 2: Wire notifier (if needed)**

Mirror Task 2's choice. If Task 2 introduced `cardNotifier` in the same package, reuse it. Else introduce one here.

- [ ] **Step 3: Write failing tests**

Three tests asserting `Type`, `UserID == request.ClientID`, `Data` keys, `RefType == "card_request"`, `RefID == request.ID` for each of the three operations. Use a recording notifier stub.

- [ ] **Step 4: Implement**

After each existing domain publish, add the corresponding `PublishGeneralNotification` block:

**`CreateRequest`** — after `PublishCardRequestCreated`:
```go
	_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  req.ClientID,
		Type:    "CARD_REQUEST_CREATED",
		Data:    map[string]string{"card_brand": req.CardBrand},
		RefType: "card_request",
		RefID:   req.ID,
	})
```

**`ApproveRequest`** — after `PublishCardRequestApproved`. The variable for the request after lookup may be a fresh local — find the right one with `ClientID` + `CardBrand` in scope (likely a `req` reloaded after the update; if not, fetch it):
```go
	_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  req.ClientID,
		Type:    "CARD_REQUEST_APPROVED",
		Data:    map[string]string{"card_brand": req.CardBrand},
		RefType: "card_request",
		RefID:   id,
	})
```

**`RejectRequest`** — after `PublishCardRequestRejected`:
```go
	_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  req.ClientID,
		Type:    "CARD_REQUEST_REJECTED",
		Data:    map[string]string{"reason": reason},
		RefType: "card_request",
		RefID:   id,
	})
```

For Approve/Reject the method probably accepts `id` not the full `req` — fetch the request via the existing `s.repo` if needed (or use whatever lookup the existing domain publish uses).

- [ ] **Step 5: Run tests + lint + commit**

```
cd card-service && go test ./... -count=1
cd card-service && golangci-lint run ./internal/service/
git add card-service/internal/service/card_request_service.go card-service/internal/service/card_request_service_test.go
git commit -m "feat(card-service): emit CARD_REQUEST_CREATED/APPROVED/REJECTED in-app notifications from service layer"
```

## Task 4: Full build/test/lint + Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Update `docs/Specification.md`**

In §19 (Kafka), add the Plan B5 paragraph after the B4 paragraph:

> **card-service** now emits `GeneralNotificationMessage` intents on `notification.general` in the `Data` form for: `CARD_CREATED`, `CARD_STATUS_CHANGED` (on block/unblock/deactivate), `CARD_TEMPORARY_BLOCKED`, `VIRTUAL_CARD_CREATED` (from gRPC handler + service layer), and `CARD_REQUEST_CREATED`, `CARD_REQUEST_APPROVED`, `CARD_REQUEST_REJECTED` (from card-request service). For cards, the recipient is `card.OwnerID` ONLY when `card.OwnerType == "client"`; authorized_person cards are skipped (resolving the underlying client owner is out of scope). For requests, the recipient is `CardRequest.ClientID`. Best-effort, after the action commits.

Update legacy `card_issued` / `card_blocked` Type rows in the General Notification Types table if present.

- [ ] **Step 2: Full build + test + lint**

```
make build
cd card-service && go test ./... -count=1
cd card-service && golangci-lint run ./...
cd contract && go test ./... -count=1
cd notification-service && go test ./... -count=1
```

Expected: all green, zero new lint warnings in B5-touched files. Pre-existing warnings elsewhere → report.

- [ ] **Step 3: Commit**

```bash
git add docs/Specification.md
git commit -m "docs: card-service in-app notification emits (Plan B5)"
```

---

## Self-Review Notes

- **Spec coverage:** all 7 card/card-request Type strings → Tasks 1–3; recipient rule (client owner only; skip authorized_person for cards) → guards at every handler emit; CardRequest.ClientID is always client → no guard needed in Task 3.
- **Placeholder scan:** Task 1 Step 1 (mock-extension) and Task 2/3 Step 1 (read service producer type) are explicit "read X" instructions — needed because the implementer should inspect actual producer field types. The cron/service-layer testability decision matches Plans B2-B4 precedent.
- **Type consistency:** `producerFacade` interface stays as-is (already has `PublishGeneralNotification`). New service-layer notifier interface(s) follow the B1/B2/B3/B4 pattern.
- **Build order:** Tasks 1–3 affect different files; each is independently committable. Task 4 is docs + verify.
