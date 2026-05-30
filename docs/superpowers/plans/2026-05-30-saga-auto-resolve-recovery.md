# Saga auto-resolve recovery — no human-review steps

**Date:** 2026-05-30
**Goal (user):** "fix everything ... don't have any step involving humans with saga, it should auto resolve."

Today `stock-service/internal/service/saga_recovery.go` leaves many stuck
OTC/exercise/accept/fund saga steps in a "needs human review" log-and-leave
state. This plan makes every reconstructable saga drive itself to a terminal
state (Completed or Compensated) idempotently, with zero human intervention.

## Core idea

A stuck saga row only exists after a hard **process crash mid-saga** — in
normal operation `saga.Execute` runs forward and, on any business failure,
rolls back fully and synchronously before returning. So recovery's job is to
re-drive a *crashed* saga to terminal.

The `saga.Saga` executor already supports **forward-resume**: `Execute` skips
any step `Recorder.IsCompleted` reports done, so re-running an assembled saga
with the *same sagaID* replays only the not-yet-completed steps. That is safe
**iff every forward step is idempotent**.

- Money steps (reserve / settle / credit / debit) — already idempotent via
  deterministic idempotency keys (account-service dedups at the ledger).
- `consume_seller_holding` — idempotent via the holding-settlement row keyed by
  the synthetic txn id.
- capital-gain steps — idempotent via `ON CONFLICT (idempotency_key)` (done
  2026-05-30, commit 3998468).
- `mark_contract_exercised` / publish — idempotent (set-to-same / outbox).
- **`upsert_buyer_holding` — the one gap.** The weighted-average merge
  double-applies on replay. Fixed here with a marker (below).

## Changes

### 1. Idempotent buyer-credit (close the one gap)
New marker table `holding_credit_markers (idempotency_key unique)`. Two new
repo methods that do marker-insert + holding mutation in **one transaction**:
- `HoldingRepository.UpsertIdempotent(ctx, holding, key)` — insert marker
  `ON CONFLICT DO NOTHING`; if inserted → weighted-avg upsert; else no-op.
- `HoldingRepository.DecrementForOwnerIdempotent(ctx, ..., key)` — if marker
  present → decrement + delete marker; else no-op.
- Same pair on `FundHoldingRepository` (fund branch of the exercise step).

Key = `otc-exercise-buyer-credit-<contractID>` (contract-scoped, stable across
replays). Forward and Backward share the key so they pair up exactly.

### 2. Rebuildable exercise saga
Refactor `OTCOfferService.ExerciseContract` to assemble the saga in a private
`buildExerciseSaga(sagaID string, c *OptionContract) (*saga.Saga, *saga.State, error)`
helper. Set `state["order_id"] = c.ID` so every persisted exercise row carries
the contract id (recovery reads `step.OrderID` to know which contract to
rebuild). `ExerciseContract` calls the helper then `Execute`.

### 3. Executor `Compensate`
Add `func (s *Saga) Compensate(ctx, state) error` to `contract/shared/saga`:
compensate every **completed** forward step in reverse order (idempotent
Backwards make re-compensation safe). Used for the rare "crashed mid-rollback"
case (compensation rows already exist → the saga was aborting, so finish the
abort rather than drive forward).

### 4. Recovery delegate
`OTCOfferService.RecoverExerciseSaga(ctx, sagaID string, contractID uint64) error`:
1. Load contract; if already `exercised` → mark rows terminal, done.
2. Rebuild saga via `buildExerciseSaga(sagaID, c)`.
3. If the saga has compensation rows (`SagaLogRepository.HasCompensations`)
   → `Compensate` (finish rollback, contract stays active).
4. Else → `Execute` (forward-resume → exercised).

### 5. Wire into SagaRecovery
Add an `exerciseRecoverer` dependency (interface) to `SagaRecovery`. Replace
the exercise-step "needs human review" cases (`reserve_strike`,
`settle_strike_buyer`, `credit_strike_seller`, `consume_seller_holding`,
`upsert_buyer_holding`, `record_seller_strike_gain`,
`record_buyer_exercise_cost`, `mark_contract_exercised`,
`publish_otc_exercise_event`) with a single `reconcileExercise` that calls the
delegate with `step.OrderID` as the contract id.

## Testing
- Unit: `UpsertIdempotent` / `DecrementForOwnerIdempotent` replay no-op (holding
  qty stable across N calls); fund variant.
- Unit: `Saga.Compensate` runs Backwards in reverse for completed steps only.
- Unit: `RecoverExerciseSaga` forward-resume (fake recorder with some completed)
  drives to exercised; rollback path leaves contract active.
- Integration (SG suite): existing SG-05/07 already prove inline compensation;
  add SG-09 (crash-resume) only if a crash-injection hook is wired — otherwise
  the unit tests cover the recovery delegate.

## Rollout
Exercise saga first (has SG tests, is what the user exercised). Then apply the
same delegate pattern to the **accept** saga and the **fund buy/sell** sagas in
follow-up commits. Crossbank / transaction-service step kinds that "should
never land in this table" stay as defensive routing-bug logs (not reachable in
practice; nothing to auto-resolve).
