# Notification Coverage — Plan B3 (credit-service wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire credit-service to emit in-app notification intents for every significant loan and installment event — migrating 2 existing hardcoded-`Title/Message` emits to the `Data` form and adding 4 new emit sites.

**Architecture:** credit-service already has `producer.PublishGeneralNotification` and `notification.general` in `EnsureTopics`. Today it emits 2 `GeneralNotificationMessage`s with hardcoded Title/Message from the gRPC handler (loan_approved, loan_rejected) via the `creditProducer` interface (already mock-able). The cron service (`internal/service/cron_service.go`) publishes domain Kafka events on installment collected/failed but no in-app notifications. This plan migrates the handler emits to the `Data` form (`LOAN_REQUEST_APPROVED`, `LOAN_REQUEST_REJECTED`) and adds `LOAN_REQUEST_SUBMITTED`, `LOAN_DISBURSED`, `INSTALLMENT_COLLECTED`, `INSTALLMENT_FAILED`.

**Tech Stack:** Go, Kafka, GORM, golangci-lint.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-coverage-expansion-design.md` §2 Loans table, §3 recipient rules. Plan B3 of 5 (B1 stock + B2 transaction shipped; B4 account, B5 card pending).

**Prerequisite:** Plan A done — `GeneralNotificationMessage` has `Data`; registry has `push` types `LOAN_REQUEST_SUBMITTED`, `LOAN_REQUEST_APPROVED`, `LOAN_REQUEST_REJECTED`, `LOAN_DISBURSED`, `INSTALLMENT_COLLECTED`, `INSTALLMENT_FAILED`.

**Target Type strings + Data keys (from the design):**
| Type | Recipient | Data keys | RefType / RefID |
|---|---|---|---|
| `LOAN_REQUEST_SUBMITTED` | borrower (`LoanRequest.ClientID`) | `loan_type, amount` | `loan_request` / loanReq.ID |
| `LOAN_REQUEST_APPROVED` | borrower | `loan_type, amount` | `loan` / loan.ID |
| `LOAN_REQUEST_REJECTED` | borrower | `loan_type, amount` | `loan_request` / loanReq.ID |
| `LOAN_DISBURSED` | borrower (`Loan.ClientID`) | `loan_number, amount, currency` | `loan` / loan.ID |
| `INSTALLMENT_COLLECTED` | borrower (`Loan.ClientID` via `Installment.LoanID`) | `amount` | `installment` / installment.ID |
| `INSTALLMENT_FAILED` | borrower | `amount, retry_deadline` | `installment` / installment.ID |

Recipient is always the loan's borrower client; there is no bank-side skip in this domain (loans always have a client borrower).

`amount` formatting: `decimal.StringFixed(2)` for currency values; `Installment.Amount` etc.

---

## File Structure

**Modified:**
- `credit-service/internal/handler/grpc_handler.go` — migrate `loan_approved` → `LOAN_REQUEST_APPROVED` + `loan_rejected` → `LOAN_REQUEST_REJECTED`; add `LOAN_REQUEST_SUBMITTED` at `CreateLoanRequest` and `LOAN_DISBURSED` at the disbursed-loan path
- `credit-service/internal/handler/grpc_handler_test.go` — extend `stubProducer` assertions (the `notifs` slice already exists); add tests for the new emits
- `credit-service/internal/service/cron_service.go` — emit `INSTALLMENT_COLLECTED` + `INSTALLMENT_FAILED` next to the existing domain publishes
- `credit-service/internal/service/cron_service_test.go` (create if it doesn't exist) — assert the cron emits
- `docs/Specification.md` — note the new/migrated emits

---

## Task 1: Migrate handler emits + add LOAN_REQUEST_SUBMITTED + LOAN_DISBURSED

**Files:**
- Modify: `credit-service/internal/handler/grpc_handler.go`
- Modify: `credit-service/internal/handler/grpc_handler_test.go`

The `creditProducer` interface (declared at the top of `grpc_handler.go`) is the type of `h.producer`. The tests inject a `*stubProducer` (`grpc_handler_test.go:174-181`) that captures emits into `notifs []kafkamsg.GeneralNotificationMessage`. This is already testable.

The existing emits in `grpc_handler.go`:
- `ApproveLoanRequest` (around line 183): emits `Type: "loan_approved"` with hardcoded Title/Message and `RefType: "loan", RefID: loan.ID`. Migrate to `LOAN_REQUEST_APPROVED` + `Data` form. Keep RefType/RefID as `loan`/loan.ID (the loan exists by then; the design's "loan" is acceptable here even though the table says `loan_request`/loanReq.ID — both are reasonable, but to match Plan B2's discipline, use `loan_request`/loanReq.ID where the loan request is the user-facing reference, and `loan`/loan.ID once a Loan exists; for APPROVED prefer `loan`/loan.ID because the loan now exists).
- `RejectLoanRequest` (around line 210): emits `Type: "loan_rejected"` with hardcoded Title/Message. Migrate to `LOAN_REQUEST_REJECTED` + `Data` form; RefType/RefID = `loan_request`/loanReq.ID.

New emits to add in the handler:
- `CreateLoanRequest`: after the request is persisted and the existing `PublishLoanRequested` domain event publish, emit `LOAN_REQUEST_SUBMITTED` to `loanReq.ClientID`, Data `{loan_type, amount}`, RefType `loan_request`, RefID `loanReq.ID`.
- `ApproveLoanRequest`: when the existing code emits `PublishLoanDisbursed` (the `loan.Status == "active"` branch, around line 175-179), ALSO emit `LOAN_DISBURSED` to `loan.ClientID`, Data `{loan_number, amount, currency}`, RefType `loan`, RefID `loan.ID`.

- [ ] **Step 1: Write the failing tests**

In `grpc_handler_test.go`, the existing `stubProducer.notifs` slice already captures emits. Find the existing test that exercises `ApproveLoanRequest` → success disbursement; it likely already asserts `len(prod.notifs) == 1`. Update / add tests:

1. **`TestCreateLoanRequest_EmitsSubmittedNotification`** — call `CreateLoanRequest`; assert `prod.notifs` contains exactly one with `Type == "LOAN_REQUEST_SUBMITTED"`, `UserID == clientID`, `Data["loan_type"] != ""`, `Data["amount"] != ""`, `RefType == "loan_request"`, `RefID == loanReq.ID`.

2. **`TestApproveLoanRequest_EmitsApprovedAndDisbursed`** — when the loan is approved and disbursed (existing happy-path test), assert `prod.notifs` contains exactly ONE `LOAN_REQUEST_APPROVED` (Data: `loan_type`, `amount`) AND exactly ONE `LOAN_DISBURSED` (Data: `loan_number`, `amount`, `currency`), in any order. The existing test asserts `len(prod.notifs) == 1` — update that to `2`, OR add a new sibling test if simpler. Match the existing fixture / setup (`newTestHandler()` etc.).

3. **`TestApproveLoanRequest_EmitsApprovedOnly_WhenNotDisbursed`** — if there is an approve path that doesn't immediately disburse (e.g. disbursement saga fails / loan remains `approved` not `active`), assert only `LOAN_REQUEST_APPROVED` is emitted (not `LOAN_DISBURSED`). Read the actual handler logic to confirm whether this path exists — if it doesn't, skip this test.

4. **`TestRejectLoanRequest_EmitsRejectedNotification`** — call `RejectLoanRequest`; assert `prod.notifs` contains exactly one `LOAN_REQUEST_REJECTED` with Data `loan_type` + `amount`, RefType `loan_request`, RefID `loanReq.ID`. The existing test may already check `len(prod.notifs)` — adapt.

Run: `cd credit-service && go test ./internal/handler/ -run TestCreateLoanRequest_EmitsSubmittedNotification -count=1 && go test ./internal/handler/ -run TestApproveLoanRequest_EmitsApprovedAndDisbursed -count=1 && go test ./internal/handler/ -run TestRejectLoanRequest_EmitsRejectedNotification -count=1` — expect FAIL.

- [ ] **Step 2: Migrate the handler emits**

Read `grpc_handler.go` around the two existing `_ = h.producer.PublishGeneralNotification(...)` blocks and replace them:

**`ApproveLoanRequest`** — replace the existing block with:
```go
	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  loanReq.ClientID,
		Type:    "LOAN_REQUEST_APPROVED",
		Data:    map[string]string{"loan_type": loan.LoanType, "amount": loan.Amount.StringFixed(2)},
		RefType: "loan",
		RefID:   loan.ID,
	})
```

**`RejectLoanRequest`** — replace with:
```go
	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  loanReq.ClientID,
		Type:    "LOAN_REQUEST_REJECTED",
		Data:    map[string]string{"loan_type": loanReq.LoanType, "amount": loanReq.Amount.StringFixed(2)},
		RefType: "loan_request",
		RefID:   loanReq.ID,
	})
```

- [ ] **Step 3: Add the new emits**

**`CreateLoanRequest`** — after the existing `PublishLoanRequested` call (around line 119-124) and the success return, add:
```go
	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  loanReq.ClientID,
		Type:    "LOAN_REQUEST_SUBMITTED",
		Data:    map[string]string{"loan_type": loanReq.LoanType, "amount": loanReq.Amount.StringFixed(2)},
		RefType: "loan_request",
		RefID:   loanReq.ID,
	})
```

**`ApproveLoanRequest`** — find the existing `PublishLoanDisbursed` call (it sits inside an `if loan.Status == "active"` branch around line 171-179). Right next to it, add a `LOAN_DISBURSED` emit:
```go
	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  loan.ClientID,
		Type:    "LOAN_DISBURSED",
		Data:    map[string]string{"loan_number": loan.LoanNumber, "amount": loan.Amount.StringFixed(2), "currency": loan.CurrencyCode},
		RefType: "loan",
		RefID:   loan.ID,
	})
```

Match the actual variable names — read the file first. The variable might be `loanReq` not `loan` for the request and `loan` for the disbursed loan; confirm at the actual call sites. Best-effort `_ =` discard, no `nil` guard needed (the handler holds the `creditProducer` interface which is always assigned from the real concrete `*kafka.Producer`).

- [ ] **Step 4: Run the tests**

Run: `cd credit-service && go test ./internal/handler/ -count=1`
Expected: all PASS (the new tests + every existing handler test, possibly with the existing notif-count expectation updated).

- [ ] **Step 5: Lint**

Run: `cd credit-service && golangci-lint run ./internal/handler/`
Expected: zero new warnings.

- [ ] **Step 6: Commit**

```bash
git add credit-service/internal/handler/grpc_handler.go credit-service/internal/handler/grpc_handler_test.go
git commit -m "feat(credit-service): emit LOAN_REQUEST_SUBMITTED/APPROVED/REJECTED + LOAN_DISBURSED in-app notifications via Data form"
```

## Task 2: cron INSTALLMENT_COLLECTED + INSTALLMENT_FAILED emits

**Files:**
- Modify: `credit-service/internal/service/cron_service.go`
- Possibly create: `credit-service/internal/service/cron_service_test.go` (if not present)

`CronService.collectDueInstallments` iterates due installments. For each:
- **Success path** (around cron_service.go:269-282): publishes `kafkamsg.InstallmentResultMessage` to `transaction.installment-collected` via `c.producer.PublishInstallmentCollected`.
- **Failure path** (around cron_service.go:123-162): sends a failure EMAIL via `c.producer.SendEmail`, then publishes `InstallmentResultMessage{Success: false, RetryDeadline: ...}` to `transaction.installment-failed` via `c.producer.PublishInstallmentFailed`. The handler has `loan` (with `loan.ClientID`), `loanID`, `amount` (string), `currencyCode`, `dueDate`, `retryDeadline`, and `installmentID` in scope at this point.

The `CronService.producer` field is the concrete `*kafka.Producer` (which already implements `PublishGeneralNotification`). For testability we have two options: (a) introduce a narrow `notifier` interface on `CronService` similar to B1/B2; or (b) keep the concrete producer and add an existing-pattern test by injecting a test producer that wraps a real kafka producer. Given the cron logic is complex (account-client calls, late-penalty etc.) and there may be no existing cron test file at all, the right call is:

- **If `cron_service_test.go` exists** and already has a mock/stub producer pattern → extend it with `notifs []kafkamsg.GeneralNotificationMessage` capture and assert.
- **If no test file exists** → introduce a narrow `cronNotifier` interface on `CronService` (one method: `PublishGeneralNotification(ctx, GeneralNotificationMessage) error`), add a `notifier cronNotifier` field, wire it from `producer` in `NewCronService` with the typed-nil guard, and use `c.notifier` for the new emits. Then add a focused test file that exercises just the emit logic with a minimal cron + injected notifier (skip the full installment-collection setup if it's too heavy).

Choose path based on what's there. If introducing the interface, name it `cronNotifier` (since `notifier` already exists in `payment_service.go` — same module, different package; package-scoped names are fine but a domain-specific name is clearer).

- [ ] **Step 1: Read the current code**

Read `cron_service.go` fully — especially `CronService` struct, `NewCronService` constructor, the success-path emit point, the failure-path emit point. Read whatever test file exists for `cron_service` (or confirm none exists by `ls credit-service/internal/service/cron_service*`). Then choose path (a) or (b) above and document the choice in the commit message.

- [ ] **Step 2: Write the failing tests**

Based on the chosen path, write tests asserting:
1. **`TestCronService_CollectsInstallment_EmitsCollectedNotification`** — when an installment is collected successfully, an `INSTALLMENT_COLLECTED` notification is emitted to `loan.ClientID` with `Data["amount"] != ""`, `RefType == "installment"`, `RefID == installment.ID`.
2. **`TestCronService_InstallmentDebitFails_EmitsFailedNotification`** — when the debit fails, an `INSTALLMENT_FAILED` is emitted to `loan.ClientID` with `Data["amount"]` and `Data["retry_deadline"]` both non-empty.

If a heavy cron-test setup is impractical, factor the emit logic into a small testable helper (e.g. `notifyInstallment(ctx, loan, installment, success, retryDeadline)`) and unit-test that helper directly with the recording notifier. Document the factoring in the commit.

Run: expect FAIL.

- [ ] **Step 3: Implement the emits**

In `cron_service.go`'s success path (right after the existing `PublishInstallmentCollected` block):
```go
	if c.producer != nil {
		_ = c.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  loan.ClientID,
			Type:    "INSTALLMENT_COLLECTED",
			Data:    map[string]string{"amount": amount},
			RefType: "installment",
			RefID:   installmentID,
		})
	}
```

(`amount` is already a string in this scope per the explore report. Use `c.notifier` instead of `c.producer` if path (b) was chosen.)

In the failure path (right after `PublishInstallmentFailed`):
```go
	if c.producer != nil {
		_ = c.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  loan.ClientID,
			Type:    "INSTALLMENT_FAILED",
			Data:    map[string]string{"amount": amount, "retry_deadline": retryDeadline},
			RefType: "installment",
			RefID:   installmentID,
		})
	}
```

Match the actual variable names. Best-effort, no error handling besides the existing log lines.

- [ ] **Step 4: Run the tests**

Run: `cd credit-service && go test ./internal/service/ -count=1`
Expected: all PASS.

- [ ] **Step 5: Lint**

Run: `cd credit-service && golangci-lint run ./internal/service/`
Expected: zero new warnings.

- [ ] **Step 6: Commit**

```bash
git add credit-service/internal/service/cron_service.go credit-service/internal/service/cron_service_test.go
git commit -m "feat(credit-service): emit INSTALLMENT_COLLECTED/FAILED in-app notifications from cron"
```

## Task 3: Full build/test/lint + Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Update `docs/Specification.md`**

In the Kafka/events section (§19, around the General Notification Types table), add:

> **credit-service** now emits `GeneralNotificationMessage` intents on `notification.general` in the `Data` form for: `LOAN_REQUEST_SUBMITTED`, `LOAN_REQUEST_APPROVED`, `LOAN_REQUEST_REJECTED`, `LOAN_DISBURSED` (from the gRPC handler), `INSTALLMENT_COLLECTED`, `INSTALLMENT_FAILED` (from the cron). Recipient is always the loan's borrower (`Loan.ClientID` / `LoanRequest.ClientID`); there is no bank-side skip. Best-effort, after the action commits.

If a legacy `loan_approved` / `loan_rejected` row exists in the spec, update it to the new names. Keep the edit concise.

- [ ] **Step 2: Full build + test + lint**

Run: `make build`
Run: `cd credit-service && go test ./... -count=1`
Run: `cd credit-service && golangci-lint run ./...`
Sanity-check: `cd contract && go test ./... -count=1`

Expected: build clean, all tests pass, zero new lint warnings in B3-touched files (`internal/handler/grpc_handler.go`, `grpc_handler_test.go`, `internal/service/cron_service.go`, `cron_service_test.go`). Pre-existing warnings elsewhere — report, don't fix.

- [ ] **Step 3: Commit**

```bash
git add docs/Specification.md
git commit -m "docs: credit-service in-app notification emits (Plan B3)"
```

---

## Self-Review Notes

- **Spec coverage:** all 6 loan/installment Type strings → Tasks 1–2; recipient rule (always client borrower, no bank skip) → uses `Loan.ClientID` / `LoanRequest.ClientID` directly; best-effort → emits stay in the existing best-effort handler/cron blocks.
- **Placeholder scan:** Task 2 Step 1 flags a real codebase fact the implementer must check (whether cron_service_test.go exists) and gives an explicit decision rule (path a vs. b) with the DONE_WITH_CONCERNS escape if a heavy cron-test setup is impractical.
- **Type consistency:** `creditProducer` interface (existing) supports `PublishGeneralNotification`; `*stubProducer` already captures `notifs`. The cron uses concrete `*kafka.Producer` or a new `cronNotifier` interface — Task 2 picks one with documentation. Data keys match the registry: `loan_type/amount` (submitted/approved/rejected), `loan_number/amount/currency` (disbursed), `amount` (collected), `amount/retry_deadline` (failed).
- **Build order:** All tasks are independently committable. Handler-side change (Task 1) and cron change (Task 2) are independent.
