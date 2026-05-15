# Notification Coverage — Plan B4 (account-service wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire account-service to emit in-app notification intents for every account lifecycle event, and fix three pre-existing publish-site gaps where domain Kafka producer methods exist but are never called.

**Architecture:** account-service already has `producer.PublishGeneralNotification` and `notification.general` in `EnsureTopics`. Today it emits ONE `GeneralNotificationMessage` with hardcoded `Title`/`Message` from the gRPC handler (`account_created`). The producer ALSO has `PublishAccountNameUpdated`, `PublishAccountLimitsUpdated`, and `PublishMaintenanceFeeCharged` methods — but NONE OF THEM are ever called. The maintenance cron has no producer at all. This plan:

1. Migrates the existing handler emit to the `Data` form (`ACCOUNT_OPENED`).
2. Adds `ACCOUNT_STATUS_CHANGED` emit in `UpdateAccountStatus` handler.
3. Wires the missing `PublishAccountNameUpdated` domain event + `ACCOUNT_NAME_UPDATED` notification in `UpdateAccountName` handler. Requires extending the `accountProducer` interface.
4. Wires the missing `PublishAccountLimitsUpdated` domain event + `ACCOUNT_LIMITS_UPDATED` notification in `UpdateAccountLimits` handler. Same interface extension.
5. Wires a producer field on `MaintenanceCronService`; calls `PublishMaintenanceFeeCharged` domain event + `MAINTENANCE_FEE_CHARGED` notification after each successful charge.

**Tech Stack:** Go, Kafka, GORM, golangci-lint.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-coverage-expansion-design.md` §2 Accounts table, §3 recipient rules. Plan B4 of 5 (B1 stock + B2 transaction + B3 credit shipped; B5 card pending).

**Prerequisite:** Plan A done — `GeneralNotificationMessage` has `Data`; registry has `push` types `ACCOUNT_OPENED`, `ACCOUNT_STATUS_CHANGED`, `ACCOUNT_NAME_UPDATED`, `ACCOUNT_LIMITS_UPDATED`, `MAINTENANCE_FEE_CHARGED`.

**Target Type strings + Data keys (from the design):**
| Type | Recipient | Data keys | RefType / RefID |
|---|---|---|---|
| `ACCOUNT_OPENED` | `account.OwnerID` | `account_number, currency` | `account` / account.ID |
| `ACCOUNT_STATUS_CHANGED` | `account.OwnerID` | `account_number, new_status` | `account` / account.ID |
| `ACCOUNT_NAME_UPDATED` | `account.OwnerID` | `account_number, new_name` | `account` / account.ID |
| `ACCOUNT_LIMITS_UPDATED` | `account.OwnerID` | `account_number, daily_limit, monthly_limit` | `account` / account.ID |
| `MAINTENANCE_FEE_CHARGED` | `account.OwnerID` | `account_number, amount, currency` | `account` / account.ID |

Recipient is the account's owner (`account.OwnerID`). **Bank-owned accounts** (`account.IsBankAccount == true`, or `OwnerID == 1_000_000_000`) → skip the notification (and skip the corresponding emails too where present, but that's pre-existing behavior).

`amount` formatting: `decimal.StringFixed(2)`. Best-effort `_ =` discard.

---

## File Structure

**Modified:**
- `account-service/internal/handler/grpc_handler.go` — migrate `account_created` → `ACCOUNT_OPENED`; add `ACCOUNT_STATUS_CHANGED`; wire missing `PublishAccountNameUpdated` + `ACCOUNT_NAME_UPDATED`; wire missing `PublishAccountLimitsUpdated` + `ACCOUNT_LIMITS_UPDATED`. Extend `accountProducer` interface.
- `account-service/internal/handler/grpc_handler_test.go` — extend `mockAccountProducer` (it already captures `generalNotificationCalls`); add `accountNameUpdatedCalls`, `accountLimitsUpdatedCalls` slices; add per-emit tests.
- `account-service/internal/service/maintenance_cron.go` — add `producer` field; wire `PublishMaintenanceFeeCharged` + `MAINTENANCE_FEE_CHARGED` after each successful charge.
- `account-service/cmd/main.go` — wire the producer into `NewMaintenanceCronService`.
- `account-service/internal/service/maintenance_cron_test.go` (create if absent) — assert the cron emits.
- `docs/Specification.md` — note new/migrated emits + the three previously-missing domain publishes now wired.

---

## Task 1: Migrate `account_created` → `ACCOUNT_OPENED` + add `ACCOUNT_STATUS_CHANGED`

**Files:**
- Modify: `account-service/internal/handler/grpc_handler.go`
- Modify: `account-service/internal/handler/grpc_handler_test.go`

The existing `CreateAccount` handler (around grpc_handler.go:311-344) emits one `GeneralNotificationMessage` with `Type: "account_created"` and hardcoded Title/Message. The `UpdateAccountStatus` handler (around line 457-477) currently publishes `AccountStatusChangedMessage` to Kafka via `PublishAccountStatusChanged` but emits NO in-app notification.

Recipient skip rule: if `account.IsBankAccount == true` OR `account.OwnerID == 1_000_000_000`, skip the in-app notification entirely. Otherwise emit. (The `account` variable has both `IsBankAccount` and `OwnerID` fields per the model.)

- [ ] **Step 1: Write the failing tests**

In `grpc_handler_test.go`, find the existing `mockAccountProducer` (which already has `generalNotificationCalls []kafkamsg.GeneralNotificationMessage`). Add tests:

1. **`TestCreateAccount_EmitsAccountOpenedNotification`** — happy-path client-owned account; assert exactly one captured notif with `Type == "ACCOUNT_OPENED"`, `UserID == account.OwnerID`, `Data["account_number"] != ""`, `Data["currency"] != ""`, `RefType == "account"`, `RefID == account.ID`, Title/Message empty.

2. **`TestCreateAccount_BankOwned_NoNotification`** — bank-owned account (`IsBankAccount: true` or `OwnerID: 1_000_000_000`); assert no `ACCOUNT_OPENED` captured (the existing domain event publish stays).

3. **`TestUpdateAccountStatus_EmitsStatusChangedNotification`** — call `UpdateAccountStatus`; assert one `ACCOUNT_STATUS_CHANGED` notif with `UserID == account.OwnerID`, `Data["account_number"] != ""`, `Data["new_status"] == account.Status`.

4. **`TestUpdateAccountStatus_BankOwned_NoNotification`** — bank account; assert no status notification.

Build using existing fixtures (`newTestHandler` or whatever the existing tests use). Run: expect FAIL (current handler emits `account_created` with Title/Message; doesn't emit on UpdateAccountStatus).

- [ ] **Step 2: Migrate `CreateAccount`'s emit**

In `grpc_handler.go`'s `CreateAccount`, find the existing emit block (~lines 337-344). Replace with:

```go
	if !account.IsBankAccount && account.OwnerID != 1_000_000_000 {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  account.OwnerID,
			Type:    "ACCOUNT_OPENED",
			Data:    map[string]string{"account_number": account.AccountNumber, "currency": account.CurrencyCode},
			RefType: "account",
			RefID:   account.ID,
		})
	}
```

Drop the legacy `Title`/`Message`. Keep the existing `PublishAccountCreated` domain event publish unchanged.

If `fmt.Sprintf` was only used in that one Title/Message, check whether `fmt` is still used elsewhere in the file; remove the import only if entirely unused.

- [ ] **Step 3: Add `ACCOUNT_STATUS_CHANGED` to `UpdateAccountStatus`**

In `UpdateAccountStatus` handler (~lines 457-477), after the existing `_ = h.producer.PublishAccountStatusChanged(ctx, ...)` call, add:

```go
	if !account.IsBankAccount && account.OwnerID != 1_000_000_000 {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  account.OwnerID,
			Type:    "ACCOUNT_STATUS_CHANGED",
			Data:    map[string]string{"account_number": account.AccountNumber, "new_status": account.Status},
			RefType: "account",
			RefID:   account.ID,
		})
	}
```

- [ ] **Step 4: Run tests + lint**

```
cd account-service && go test ./internal/handler/ -count=1
cd account-service && golangci-lint run ./internal/handler/
```
Expected: PASS / clean.

- [ ] **Step 5: Commit**

```bash
git add account-service/internal/handler/grpc_handler.go account-service/internal/handler/grpc_handler_test.go
git commit -m "feat(account-service): emit ACCOUNT_OPENED + ACCOUNT_STATUS_CHANGED in-app notifications via Data form"
```

## Task 2: Wire `PublishAccountNameUpdated` + `ACCOUNT_NAME_UPDATED`

**Files:**
- Modify: `account-service/internal/handler/grpc_handler.go`
- Modify: `account-service/internal/handler/grpc_handler_test.go`

Today `UpdateAccountName` handler (~lines 425-439) calls the service, fetches the updated account, returns the response — but never publishes the domain `account.name-updated` Kafka event even though `producer.PublishAccountNameUpdated` EXISTS. The `accountProducer` interface (in grpc_handler.go, around line 56-61) does NOT include `PublishAccountNameUpdated`. We need to:
1. Extend the `accountProducer` interface to declare `PublishAccountNameUpdated(ctx, kafkamsg.AccountNameUpdatedMessage) error` — confirm the exact message-struct type name by grepping for `AccountNameUpdated` in `contract/kafka/`.
2. Extend the `mockAccountProducer` test stub to satisfy the new interface method + capture into a slice.
3. In `UpdateAccountName` handler, call the new method (fills the missing domain publish gap) AND emit `ACCOUNT_NAME_UPDATED`.

- [ ] **Step 1: Find the message struct**

Read `contract/kafka/messages.go` (or wherever `AccountNameUpdatedMessage` is declared) and quote the exact struct name + fields. Likely something like:
```go
type AccountNameUpdatedMessage struct {
    AccountNumber string
    NewName       string
    // ...
}
```

If the struct doesn't exist, ADD it — make minimal fields (AccountNumber + NewName) and the matching topic constant.

- [ ] **Step 2: Extend `accountProducer` interface**

In `grpc_handler.go`, the interface (~line 56-61) currently has:
```go
type accountProducer interface {
    PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error
    PublishAccountStatusChanged(ctx context.Context, msg kafkaprod.AccountStatusChangedMsg) error
    PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
    SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error
}
```

Add `PublishAccountNameUpdated(ctx context.Context, msg kafkamsg.AccountNameUpdatedMessage) error`. The concrete `*kafka.Producer` already implements this method per the explore report.

- [ ] **Step 3: Extend the test stub**

In `grpc_handler_test.go`, add to `mockAccountProducer`:
```go
accountNameUpdatedCalls []kafkamsg.AccountNameUpdatedMessage
publishAccountNameUpdatedFn func(ctx context.Context, msg kafkamsg.AccountNameUpdatedMessage) error
```
plus the method:
```go
func (m *mockAccountProducer) PublishAccountNameUpdated(ctx context.Context, msg kafkamsg.AccountNameUpdatedMessage) error {
    m.accountNameUpdatedCalls = append(m.accountNameUpdatedCalls, msg)
    if m.publishAccountNameUpdatedFn != nil { return m.publishAccountNameUpdatedFn(ctx, msg) }
    return nil
}
```

- [ ] **Step 4: Write the failing tests**

1. **`TestUpdateAccountName_PublishesDomainEvent`** — call `UpdateAccountName`; assert `len(m.accountNameUpdatedCalls) == 1`, with the right account number and new name.
2. **`TestUpdateAccountName_EmitsNameUpdatedNotification`** — assert one `ACCOUNT_NAME_UPDATED` captured with `Type`, `UserID`, `Data: {account_number, new_name}`, `RefType: "account"`, `RefID: account.ID`.
3. **`TestUpdateAccountName_BankOwned_NoNotification`** — bank account: domain event still publishes, but no in-app notification.

Run: expect FAIL.

- [ ] **Step 5: Implement**

In `UpdateAccountName` handler — after the existing `h.accountService.UpdateAccountName(...)` succeeds and `account, err := h.accountService.GetAccount(req.Id)` returns the updated account — add:

```go
	_ = h.producer.PublishAccountNameUpdated(ctx, kafkamsg.AccountNameUpdatedMessage{
		AccountNumber: account.AccountNumber,
		NewName:       account.AccountName,
	})
	if !account.IsBankAccount && account.OwnerID != 1_000_000_000 {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  account.OwnerID,
			Type:    "ACCOUNT_NAME_UPDATED",
			Data:    map[string]string{"account_number": account.AccountNumber, "new_name": account.AccountName},
			RefType: "account",
			RefID:   account.ID,
		})
	}
```

Match the actual struct field names of `AccountNameUpdatedMessage` (NewName might be just `Name`).

- [ ] **Step 6: Run tests + lint + commit**

```
cd account-service && go test ./internal/handler/ -count=1
cd account-service && golangci-lint run ./internal/handler/
```

```bash
git add account-service/internal/handler/grpc_handler.go account-service/internal/handler/grpc_handler_test.go
git commit -m "feat(account-service): wire missing PublishAccountNameUpdated + emit ACCOUNT_NAME_UPDATED notification"
```

## Task 3: Wire `PublishAccountLimitsUpdated` + `ACCOUNT_LIMITS_UPDATED`

**Files:**
- Modify: `account-service/internal/handler/grpc_handler.go`
- Modify: `account-service/internal/handler/grpc_handler_test.go`

Symmetric to Task 2 — `UpdateAccountLimits` handler (~lines 441-455) lacks both the domain publish AND the notification. Wire both.

- [ ] **Step 1: Confirm `AccountLimitsUpdatedMessage` struct shape**

Find its declaration in `contract/kafka/`. Likely fields: `AccountNumber`, `DailyLimit`, `MonthlyLimit`. If absent, add it. Confirm `producer.PublishAccountLimitsUpdated` exists (it does per explore).

- [ ] **Step 2: Extend `accountProducer` interface + test stub**

Same pattern as Task 2 — add `PublishAccountLimitsUpdated(ctx, kafkamsg.AccountLimitsUpdatedMessage) error` to the interface and the mock.

- [ ] **Step 3: Write failing tests**

1. **`TestUpdateAccountLimits_PublishesDomainEvent`** — domain call captured.
2. **`TestUpdateAccountLimits_EmitsLimitsUpdatedNotification`** — `Type == "ACCOUNT_LIMITS_UPDATED"`, `Data: {account_number, daily_limit, monthly_limit}` (limits as `StringFixed(2)`), `RefType: "account"`, `RefID: account.ID`.
3. **`TestUpdateAccountLimits_BankOwned_NoNotification`** — bank: domain publish only, no notification.

- [ ] **Step 4: Implement**

In `UpdateAccountLimits` handler, after the update + GetAccount, add:

```go
	_ = h.producer.PublishAccountLimitsUpdated(ctx, kafkamsg.AccountLimitsUpdatedMessage{
		AccountNumber: account.AccountNumber,
		DailyLimit:    account.DailyLimit.StringFixed(2),
		MonthlyLimit:  account.MonthlyLimit.StringFixed(2),
	})
	if !account.IsBankAccount && account.OwnerID != 1_000_000_000 {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  account.OwnerID,
			Type:    "ACCOUNT_LIMITS_UPDATED",
			Data:    map[string]string{"account_number": account.AccountNumber, "daily_limit": account.DailyLimit.StringFixed(2), "monthly_limit": account.MonthlyLimit.StringFixed(2)},
			RefType: "account",
			RefID:   account.ID,
		})
	}
```

Adjust field types/names to match the actual `AccountLimitsUpdatedMessage` (limits might be `decimal.Decimal` rather than string in the message — match it).

- [ ] **Step 5: Run tests + lint + commit**

```
cd account-service && go test ./internal/handler/ -count=1
cd account-service && golangci-lint run ./internal/handler/
git add account-service/internal/handler/grpc_handler.go account-service/internal/handler/grpc_handler_test.go
git commit -m "feat(account-service): wire missing PublishAccountLimitsUpdated + emit ACCOUNT_LIMITS_UPDATED notification"
```

## Task 4: Wire producer on MaintenanceCronService + emit MAINTENANCE_FEE_CHARGED

**Files:**
- Modify: `account-service/internal/service/maintenance_cron.go`
- Modify: `account-service/cmd/main.go`
- Create or modify: `account-service/internal/service/maintenance_cron_test.go`

`MaintenanceCronService` currently has NO producer field; its constructor in `main.go` doesn't pass one. The producer ALREADY has `PublishMaintenanceFeeCharged` but it is never called. After each successful `ledgerSvc.Transfer(...)` in `chargeMaintenanceFees`, we need to:
1. Publish the `MaintenanceFeeChargedMessage` domain event.
2. Emit `MAINTENANCE_FEE_CHARGED` notification (bank skip not strictly necessary since the cron query already filters `is_bank_account = false`, but apply the same defensive `if !acc.IsBankAccount` guard).

Like the credit cron, the producer is concrete. Use the same `cronNotifier`-style interface pattern OR use the concrete producer directly with a small `notifyMaintenance` helper plus a narrow interface for testing.

**Decision:** introduce a narrow `maintenanceNotifier` interface field on `MaintenanceCronService` (one method: `PublishGeneralNotification`); add another interface for the domain publish OR keep them in one combined `maintenanceProducer` interface:

```go
type maintenanceProducer interface {
    PublishMaintenanceFeeCharged(ctx context.Context, msg kafkamsg.MaintenanceFeeChargedMessage) error
    PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}
```

Wire it with the typed-nil guard. Adjust `main.go` to pass the existing `*kafka.Producer` into the constructor.

- [ ] **Step 1: Confirm `MaintenanceFeeChargedMessage` struct shape**

Find in `contract/kafka/`. Likely fields: `AccountNumber`, `Amount`, `Currency`. Match the existing field shape.

- [ ] **Step 2: Add the producer field + constructor wiring**

In `maintenance_cron.go`, define `maintenanceProducer` interface as above. Add a `producer maintenanceProducer` field to `MaintenanceCronService`. Update `NewMaintenanceCronService` signature to accept `producer *kafka.Producer` (concrete, then typed-nil guard into the interface field — same pattern as B2/B3 cron). Update `cmd/main.go` line ~112-113 to pass the existing producer.

- [ ] **Step 3: Emit at the charge site**

In `chargeMaintenanceFees`, after the successful `s.ledgerSvc.Transfer(...)` (the `charged++` line ~80), add:

```go
	if s.producer != nil {
		_ = s.producer.PublishMaintenanceFeeCharged(ctx, kafkamsg.MaintenanceFeeChargedMessage{
			AccountNumber: acc.AccountNumber,
			Amount:        acc.MaintenanceFee.StringFixed(2),
			Currency:      acc.CurrencyCode,
		})
		if !acc.IsBankAccount {
			_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
				UserID:  acc.OwnerID,
				Type:    "MAINTENANCE_FEE_CHARGED",
				Data:    map[string]string{"account_number": acc.AccountNumber, "amount": acc.MaintenanceFee.StringFixed(2), "currency": acc.CurrencyCode},
				RefType: "account",
				RefID:   acc.ID,
			})
		}
	}
```

Match the actual `MaintenanceFeeChargedMessage` field types.

- [ ] **Step 4: Tests**

Create `maintenance_cron_test.go` (if absent) or extend it. Focus on the emit logic — if the full cron is too heavy to test directly, factor a small helper `notifyMaintenance(ctx, acc *model.Account)` and test it directly with a recording producer stub satisfying `maintenanceProducer`. Assert: one domain `MaintenanceFeeChargedMessage` per charged account; one `MAINTENANCE_FEE_CHARGED` notification per non-bank account.

Run + lint. Build to confirm `main.go` change compiles.

- [ ] **Step 5: Commit**

```bash
git add account-service/internal/service/maintenance_cron.go account-service/internal/service/maintenance_cron_test.go account-service/cmd/main.go
git commit -m "feat(account-service): wire producer on maintenance cron; emit MaintenanceFeeCharged + MAINTENANCE_FEE_CHARGED notification"
```

## Task 5: Full build/test/lint + Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Update `docs/Specification.md`**

In §19 (Kafka), add:

> **account-service** now emits `GeneralNotificationMessage` intents on `notification.general` in the `Data` form for: `ACCOUNT_OPENED` (on create), `ACCOUNT_STATUS_CHANGED` (on status update), `ACCOUNT_NAME_UPDATED` (on rename), `ACCOUNT_LIMITS_UPDATED` (on limit change), `MAINTENANCE_FEE_CHARGED` (per monthly cron charge). Recipient is the account owner (`account.OwnerID`); bank-owned accounts (`is_bank_account` or owner id `1_000_000_000`) are skipped. **Plan B4 also closed three pre-existing publish-site gaps**: `account.name-updated`, `account.limits-updated`, and `account.maintenance-charged` domain events are now published (the producer methods existed but were never called). Best-effort, after the action commits.

Update the General Notification Types table if it has rows for any of the new types. Update legacy `account_created` row if it exists.

- [ ] **Step 2: Full build + test + lint**

```
make build
cd account-service && go test ./... -count=1
cd account-service && golangci-lint run ./...
cd contract && go test ./... -count=1
```

Expected: all green; no new lint warnings in B4-touched files. Fix lint in touched files only; report (don't fix) pre-existing warnings elsewhere.

- [ ] **Step 3: Commit**

```bash
git add docs/Specification.md
git commit -m "docs: account-service in-app notification emits + closed domain-publish gaps (Plan B4)"
```

---

## Self-Review Notes

- **Spec coverage:** All 5 account Type strings → Tasks 1–4. The three pre-existing publish-site gaps (`name-updated`, `limits-updated`, `maintenance-charged`) → Tasks 2, 3, 4 respectively — they wire the missing producer method call + the notification side-by-side. Bank-owned skip → checks on `IsBankAccount || OwnerID == 1_000_000_000` at every emit.
- **Placeholder scan:** Task 4 Step 4 flags a real codebase fact (cron test infrastructure may not exist) and gives the helper-factor escape, matching the Plan B3 cron task's pattern. Task 2/3 Step 1 ("read the message struct") is a precise instruction — the implementer must check.
- **Type consistency:** `accountProducer` interface gains two methods (Tasks 2, 3); `mockAccountProducer` extends in lock-step. `maintenanceProducer` (Task 4) is a new narrow interface for the cron. Data keys match the registry.
- **Build order:** Tasks 1–3 affect only the handler; Task 4 affects the cron + main.go (independent). All are independently committable.
