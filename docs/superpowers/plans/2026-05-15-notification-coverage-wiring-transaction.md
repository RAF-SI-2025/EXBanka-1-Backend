# Notification Coverage — Plan B2 (transaction-service wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire transaction-service to emit in-app notification intents for payment and transfer lifecycle events — migrating the 3 existing hardcoded-`Title/Message` emits to the `Data` form and adding the failure-path emits.

**Architecture:** transaction-service already has `producer.PublishGeneralNotification` and `notification.general` in `EnsureTopics`. It already emits 3 `GeneralNotificationMessage`s with hardcoded `Title`/`Message`. This plan migrates those to the `Data map[string]string` form (so notification-service renders them via the Plan A template registry) and adds `PAYMENT_FAILED` / `TRANSFER_FAILED`. To make the emits unit-testable, introduce a narrow `notifier` interface field on `PaymentService` and `TransferService` (the `producer` field is the concrete `*kafka.Producer` and tests currently pass `nil`) — same precedent as Plan B1 Task 5's `otcNotifier`.

**Tech Stack:** Go, Kafka (`segmentio/kafka-go` via `contract/shared`), GORM, golangci-lint.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-coverage-expansion-design.md` (§2 Money movement table, §3 recipient rules). This is Plan B2 of 5 (B1 stock shipped; B3 credit, B4 account, B5 card pending).

**Prerequisite:** Plan A done — `GeneralNotificationMessage` has `Data map[string]string`; the registry has `push` types `PAYMENT_SENT`, `PAYMENT_RECEIVED`, `PAYMENT_FAILED`, `TRANSFER_SENT`, `TRANSFER_RECEIVED`, `TRANSFER_FAILED`.

**Target Type strings + Data keys (from the design §2):**
| Type | recipient | Data keys |
|---|---|---|
| `PAYMENT_SENT` | sender (fromAcct owner) | `amount, to_account` |
| `PAYMENT_RECEIVED` | receiver (toAcct owner) | `amount, from_account` |
| `PAYMENT_FAILED` | sender | `amount, failure_reason` |
| `TRANSFER_SENT` | sender (fromAcct owner) | `amount, to_account` |
| `TRANSFER_RECEIVED` | receiver (toAcct owner) | `final_amount, from_account` |
| `TRANSFER_FAILED` | sender | `amount, failure_reason` |

`RefType`/`RefID`: `"payment"`/payment.ID, `"transfer"`/transfer.ID. Recipient rule: emit only when the resolved owner id is non-zero AND not a bank-owned account — `accountpb.AccountResponse` exposes the owner id via `GetOwnerId()`; treat owner id `0` (and the bank sentinel `1_000_000_000`) as "skip". If `AccountResponse` exposes `is_bank_account` / similar, prefer that; otherwise the `owner_id == 0 || owner_id == 1_000_000_000` check is the rule.

---

## File Structure

**Modified:**
- `transaction-service/internal/service/payment_service.go` — add `notifier` interface + field; migrate `publishPaymentNotifications` to `Data` form; add `PAYMENT_FAILED` at the failure path
- `transaction-service/internal/service/transfer_service.go` — add `notifier` field; migrate `publishTransferNotification` to `Data` form + emit `TRANSFER_RECEIVED`; add `TRANSFER_FAILED` at the failure path
- `transaction-service/internal/service/payment_service_test.go` — recording notifier stub; assert emits
- `transaction-service/internal/service/transfer_service_test.go` — recording notifier stub; assert emits
- `docs/Specification.md` — note the migrated/new emits

---

## Task 1: `notifier` interface + field on both services

**Files:**
- Modify: `transaction-service/internal/service/payment_service.go`
- Modify: `transaction-service/internal/service/transfer_service.go`

- [ ] **Step 1: Add the `notifier` interface**

In `payment_service.go`, near the top type declarations, add:

```go
// notifier is the narrow slice of the Kafka producer the service uses for
// in-app notification intents. A separate interface (not the concrete
// *kafka.Producer) so unit tests can inject a recording stub.
type notifier interface {
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}
```

(`kafkamsg` is the existing alias for `github.com/exbanka/contract/kafka` in this file.) Define it once in `payment_service.go`; `transfer_service.go` is in the same package and reuses it.

- [ ] **Step 2: Add the `notifier` field + constructor wiring on `PaymentService`**

`PaymentService` has a `producer *kafka.Producer` field and a `NewPaymentService(...)` constructor. Add a `notifier notifier` field. In `NewPaymentService`, after the struct is built, set the notifier from the producer **with a typed-nil guard** (the producer param can be `nil` in tests — assigning a nil `*kafka.Producer` into the interface makes a non-nil interface holding a nil pointer, which panics on call):

```go
	if producer != nil {
		s.notifier = producer
	}
```

(Adjust to however the constructor builds `s` — if it returns a literal, restructure to `s := &PaymentService{...}; if producer != nil { s.notifier = producer }; return s`.)

- [ ] **Step 3: Same for `TransferService`**

`TransferService` likewise has `producer *kafka.Producer` + `NewTransferService(...)`. Add a `notifier notifier` field; wire it the same typed-nil-guarded way in the constructor.

- [ ] **Step 4: Build**

Run: `cd transaction-service && go build ./...`
Expected: builds clean (the field is unused so far; Go allows unused struct fields).

- [ ] **Step 5: Commit**

```bash
git add transaction-service/internal/service/payment_service.go transaction-service/internal/service/transfer_service.go
git commit -m "refactor(transaction-service): add notifier interface field for testable notification emits"
```

## Task 2: Migrate payment notifications to `Data` form + `PAYMENT_FAILED`

**Files:**
- Modify: `transaction-service/internal/service/payment_service.go`
- Modify: `transaction-service/internal/service/payment_service_test.go`

The existing helper `publishPaymentNotifications(ctx, payment)` (around payment_service.go:310-340) fetches `fromAcct` and `toAcct` via `s.accountClient.GetAccountByNumber` and emits two `GeneralNotificationMessage`s with hardcoded `Title`/`Message` and `Type: "money_sent"` / `"money_received"`, via `s.producer.PublishGeneralNotification`.

- [ ] **Step 1: Write the failing test**

In `payment_service_test.go`, add a recording notifier stub (place near the existing mocks):

```go
type recordingNotifier struct {
	notifs []kafkamsg.GeneralNotificationMessage
}

func (r *recordingNotifier) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.notifs = append(r.notifs, m)
	return nil
}
```

(Import `kafkamsg "github.com/exbanka/contract/kafka"` in the test file if not already imported.) Add a test that a successful payment between two client-owned accounts emits one `PAYMENT_SENT` to the sender's owner and one `PAYMENT_RECEIVED` to the receiver's owner, each with a non-empty `Data` map (`amount` + `to_account`/`from_account`), `RefType == "payment"`, `RefID == payment.ID`; and that a payment from/to a bank-owned account (owner id `0` or `1_000_000_000`) emits nothing for that side. Build the `PaymentService` exactly as the nearest existing `ExecutePayment` success test does, but set `svc.notifier = rec` (a `*recordingNotifier`) after construction, and use the existing `mockAccountClientForPayment` (or equivalent) with `ownerOverrides` so the from/to accounts resolve to distinct client owner ids. Copy the construction block from the nearest passing `ExecutePayment` test.

- [ ] **Step 2: Run to confirm failure**

Run: `cd transaction-service && go test ./internal/service/ -run TestPaymentService -count=1`
Expected: FAIL — the helper still emits `money_sent`/`money_received` with no `Data`, via `s.producer` not `s.notifier`.

- [ ] **Step 3: Migrate `publishPaymentNotifications`**

Rewrite the helper so it:
- emits via `s.notifier` (guard `if s.notifier == nil { return }` at the top — keep the existing best-effort `_ =` discard);
- for the **sender** (fromAcct): skip if `fromAcct.GetOwnerId() == 0 || fromAcct.GetOwnerId() == 1_000_000_000`; otherwise emit `Type: "PAYMENT_SENT"`, `Data: map[string]string{"amount": payment.InitialAmount.StringFixed(2), "to_account": payment.ToAccountNumber}`, `RefType: "payment"`, `RefID: payment.ID`;
- for the **receiver** (toAcct): skip on the same bank/zero check; emit `Type: "PAYMENT_RECEIVED"`, `Data: map[string]string{"amount": payment.InitialAmount.StringFixed(2), "from_account": payment.FromAccountNumber}`, same `RefType`/`RefID`.

Drop the `Title`/`Message` fields entirely. Keep the existing `GetAccountByNumber` lookups (they already provide `GetOwnerId()`).

- [ ] **Step 4: Add the `PAYMENT_FAILED` emit**

Read `ExecutePayment` and find the failure path — where the saga `executeWithSaga(...)` returns an error and the code marks the payment failed / publishes `transaction.payment-failed` (the `Payment` model has a `FailureReason` field; the failed-status update is in that block). **After** the payment is persisted as failed (after the DB write, best-effort, before `return`), add an emit to the **sender** only:

```go
	if s.notifier != nil {
		if fromAcct, e := s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: payment.FromAccountNumber}); e == nil && fromAcct != nil &&
			fromAcct.GetOwnerId() != 0 && fromAcct.GetOwnerId() != 1_000_000_000 {
			_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
				UserID:  fromAcct.GetOwnerId(),
				Type:    "PAYMENT_FAILED",
				Data:    map[string]string{"amount": payment.InitialAmount.StringFixed(2), "failure_reason": payment.FailureReason},
				RefType: "payment",
				RefID:   payment.ID,
			})
		}
	}
```

Match the actual variable names / failure-block structure in the file. If the failure path does not have a clear post-persist point (e.g. it `return`s the saga error immediately without persisting a failed status), persist-then-emit is out of scope for this plan — in that case emit right before the `return` of the failure branch with whatever `FailureReason` is in scope, and note it DONE_WITH_CONCERNS.

- [ ] **Step 5: Extend the failing test + add a failure test**

Add a test that a payment whose saga fails emits one `PAYMENT_FAILED` to the sender with `Data["failure_reason"]` non-empty. Use the existing mock-account-client failure injection (`failOnCall`) pattern from the neighbouring failure tests.

- [ ] **Step 6: Run the tests**

Run: `cd transaction-service && go test ./internal/service/ -run TestPaymentService -count=1`
Expected: PASS (new tests + all existing payment tests).

- [ ] **Step 7: Commit**

```bash
git add transaction-service/internal/service/payment_service.go transaction-service/internal/service/payment_service_test.go
git commit -m "feat(transaction-service): emit PAYMENT_SENT/RECEIVED/FAILED in-app notifications via Data form"
```

## Task 3: Migrate transfer notification to `Data` form + `TRANSFER_RECEIVED` + `TRANSFER_FAILED`

**Files:**
- Modify: `transaction-service/internal/service/transfer_service.go`
- Modify: `transaction-service/internal/service/transfer_service_test.go`

The existing helper `publishTransferNotification(ctx, transfer)` (around transfer_service.go:450-465) fetches `fromAcct` only and emits ONE `GeneralNotificationMessage` with hardcoded `Title`/`Message`, `Type: "money_sent"`.

- [ ] **Step 1: Write the failing test**

In `transfer_service_test.go`, add a `recordingNotifier` stub (same shape as Task 2's — if the test files share a package and Task 2 already added it to `payment_service_test.go`, reuse it; both `_test.go` files are in the same `service` package, so define it once). Add a test that a successful transfer emits one `TRANSFER_SENT` to the fromAcct owner and one `TRANSFER_RECEIVED` to the toAcct owner, with the correct `Data` keys (`amount`+`to_account`; `final_amount`+`from_account`), `RefType == "transfer"`, `RefID == transfer.ID`. Build the `TransferService` as the nearest existing `ExecuteTransfer` success test does, set `svc.notifier = rec`, and use `mockAccountClientForTransfer` with `ownerOverrides` for distinct owners. Note `mockAccountClientForTransfer.GetAccountByNumber` currently only returns the sender — confirm it returns a sensible owner for the `to` account too (extend `ownerOverrides` coverage in the test).

- [ ] **Step 2: Run to confirm failure**

Run: `cd transaction-service && go test ./internal/service/ -run TestTransferService -count=1`
Expected: FAIL.

- [ ] **Step 3: Migrate `publishTransferNotification`**

Rewrite the helper so it:
- guards `if s.notifier == nil { return }`;
- fetches BOTH `fromAcct` and `toAcct` via `s.accountClient.GetAccountByNumber`;
- emits `TRANSFER_SENT` to the fromAcct owner (skip on `owner == 0 || owner == 1_000_000_000`) with `Data: {"amount": transfer.InitialAmount.StringFixed(2), "to_account": transfer.ToAccountNumber}`;
- emits `TRANSFER_RECEIVED` to the toAcct owner (same skip) with `Data: {"final_amount": transfer.FinalAmount.StringFixed(2), "from_account": transfer.FromAccountNumber}`;
- `RefType: "transfer"`, `RefID: transfer.ID`; drop `Title`/`Message`.

(`transfer.FinalAmount` is the converted amount for cross-currency transfers and equals `InitialAmount` for same-currency — correct for what the receiver sees.)

- [ ] **Step 4: Add the `TRANSFER_FAILED` emit**

Read `ExecuteTransfer`, find the failure path (saga error → mark transfer failed, `Transfer.FailureReason`, `transaction.transfer-failed` publish). After the failed status is persisted, best-effort emit `TRANSFER_FAILED` to the fromAcct owner: `Data: {"amount": transfer.InitialAmount.StringFixed(2), "failure_reason": transfer.FailureReason}`, `RefType: "transfer"`, `RefID: transfer.ID`. Same skip rule and same DONE_WITH_CONCERNS escape as Task 2 Step 4 if there's no clean post-persist point.

- [ ] **Step 5: Add a failure test**

Test that a failed transfer emits one `TRANSFER_FAILED` to the sender. Use the `failOnCall` injection from neighbouring failure tests.

- [ ] **Step 6: Run the tests**

Run: `cd transaction-service && go test ./internal/service/ -run TestTransferService -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add transaction-service/internal/service/transfer_service.go transaction-service/internal/service/transfer_service_test.go
git commit -m "feat(transaction-service): emit TRANSFER_SENT/RECEIVED/FAILED in-app notifications via Data form"
```

## Task 4: Full build/test/lint + Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Update `docs/Specification.md`**

In the Kafka/events section (§19): note that transaction-service now emits `GeneralNotificationMessage` intents on `notification.general` in the **`Data` form** for `PAYMENT_SENT/RECEIVED/FAILED` and `TRANSFER_SENT/RECEIVED/FAILED` — sender/receiver resolved via account-service `GetAccountByNumber`, bank-owned accounts (owner id `0` / `1_000_000_000`) skipped, best-effort after the saga commits. If the spec previously documented the legacy `money_sent`/`money_received` notification types, update them to the new names.

- [ ] **Step 2: Full build + test + lint**

Run: `cd transaction-service && go build ./... && go test ./... -count=1 && golangci-lint run ./...`
Expected: build clean, all tests pass, no new lint warnings in touched files. Fix any lint warning in files this plan modified; report (don't fix) pre-existing warnings elsewhere.

- [ ] **Step 3: Commit**

```bash
git add docs/Specification.md
git commit -m "docs: transaction-service in-app notification emits (Plan B2)"
```

---

## Self-Review Notes

- **Spec coverage:** all 6 money-movement Type strings (`PAYMENT_SENT/RECEIVED/FAILED`, `TRANSFER_SENT/RECEIVED/FAILED`) → Tasks 2–3; recipient rule (resolve account→owner, skip bank/zero owner) → the skip checks in every emit; two-party emits → payment emits 2, transfer emits 2; best-effort after commit → emits stay in the existing post-commit helpers / failure branches.
- **Placeholder scan:** Task 2 Step 4 / Task 3 Step 4 flag a genuine unknown (whether the failure path has a clean post-persist point) with an explicit DONE_WITH_CONCERNS escape — a real codebase fact the implementer checks, not hand-waving. Test steps say "copy the construction block from the nearest existing test" — an exact instruction, the test-fixture boilerplate is too long to inline and varies.
- **Type consistency:** `notifier` interface (Task 1) is the field type used by the emit helpers in Tasks 2–3; `recordingNotifier` test stub satisfies it; `PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage) error` matches the concrete `*kafka.Producer` method.
- **Build order:** Task 1 ends compiling (new field unused — Go allows it). Tasks 2–3 are independently committable.
