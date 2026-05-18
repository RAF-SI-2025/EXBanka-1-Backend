# Design: Single Saga Abstraction (Spec B1)

**Status:** Approved
**Date:** 2026-04-27
**Scope:** Clean-break refactor in `stock-service`. Pure refactor — no behavior change.

## Problem

`stock-service` ships two legacy saga abstractions side-by-side with `shared.Saga`:

1. **`SagaExecutor`** (`stock-service/internal/service/saga_helper.go:27-112`) — imperative `RunStep` / `RunCompensation` API used by older placement code paths and the recovery loop in `saga_recovery.go:145-196`.

2. **`CrossbankSagaExecutor`** (different file) — phase-based (`PhaseReserveBuyerFunds`, `PhaseTransferFunds`, etc.) used by `crossbank_accept_saga.go`, `crossbank_exercise_saga.go`, `crossbank_expire_saga.go`.

OTC and fund sagas have already migrated to `shared.Saga` (commits `f6e8cc7`, `9498121`, `be4f01a`). The recovery switch in `saga_recovery.go` matches step names as **string literals** with a `default:` that logs and skips — meaning a misspelled or new step name silently falls through. Saga IDs are minted by callers, leading to the "sub-saga IDs must be ≤36 chars" bug class (commit `9498121`).

## Goal

One saga abstraction (`shared.Saga`). Step names are a closed enum with compile-time-checked exhaustiveness in the recovery switch. Saga IDs are minted by the runner, never by callers.

## Approach

### Delete both legacy abstractions

`SagaExecutor` and `CrossbankSagaExecutor` are removed entirely. Crossbank sagas are rewritten as ordered `shared.Saga` steps. Each old phase becomes 1–N steps with `Pivot: true` on the last step of the phase to mark a compensation boundary.

### `StepKind` typed enum (`contract/shared/saga/steps.go`)

```go
package saga

type StepKind string

const (
    // Crossbank accept
    StepReserveBuyerFunds   StepKind = "reserve_buyer_funds"
    StepReserveSellerShares StepKind = "reserve_seller_shares"
    StepDebitBuyer          StepKind = "debit_buyer"
    StepCreditSeller        StepKind = "credit_seller"
    StepTransferOwnership   StepKind = "transfer_ownership"
    StepFinalizeAccept      StepKind = "finalize_accept"

    // Crossbank exercise
    StepDebitStrike    StepKind = "debit_strike"
    StepCreditStrike   StepKind = "credit_strike"
    StepDeliverShares  StepKind = "deliver_shares"

    // Crossbank expire
    StepRefundReservation StepKind = "refund_reservation"
    StepMarkExpired       StepKind = "mark_expired"

    // OTC + Fund (migrated, listed for completeness)
    StepReserveAndContract StepKind = "reserve_and_contract"
    StepReservePremium     StepKind = "reserve_premium"
    StepReserveStrike      StepKind = "reserve_strike"
    StepSettlePremiumBuyer StepKind = "settle_premium_buyer"
    StepSettleStrikeBuyer  StepKind = "settle_strike_buyer"
    StepConsumeSellerHolding StepKind = "consume_seller_holding"
    StepDebitSource        StepKind = "debit_source"
    StepCreditTarget       StepKind = "credit_target"
    StepDebitFund          StepKind = "debit_fund"
    StepCreditFund         StepKind = "credit_fund"
    StepCreditPremiumSeller StepKind = "credit_premium_seller"
    StepCreditStrikeSeller  StepKind = "credit_strike_seller"
    StepUpsertPosition     StepKind = "upsert_position"
)

var allSteps = map[StepKind]struct{}{
    StepReserveBuyerFunds: {}, StepReserveSellerShares: {},
    // ... every constant above
}

// MustStep panics at startup if the kind is not a registered constant.
// Use in saga construction to catch typos at process start.
func MustStep(k StepKind) StepKind {
    if _, ok := allSteps[k]; !ok {
        panic(fmt.Sprintf("unknown StepKind: %s", k))
    }
    return k
}
```

`shared.Saga.Step.Name` is changed from `string` to `StepKind`.

### Recovery switch with panicking default

```go
func reconcileStep(step *SagaLog) error {
    switch step.StepName {
    case StepReserveBuyerFunds:
        return reconcileReserveBuyerFunds(step)
    case StepReserveSellerShares:
        return reconcileReserveSellerShares(step)
    // ... every StepKind constant
    default:
        panic(fmt.Sprintf("recovery: unhandled StepKind %q — add case to switch", step.StepName))
    }
}
```

Go's type system does not enforce switch exhaustiveness on string-typed enums, so we use a panicking `default`. This is intentional: a missing case in production triggers a fast crash with a clear message rather than silently skipping recovery.

### Saga ID minted by runner

`shared.Saga.NewSaga()` no longer accepts an external ID. It generates an internal UUID. Callers receive it via `s.ID()`.

```go
// Before
sagaID := uuid.NewString()
s := shared.NewSaga(sagaID, recorder)

// After
s := shared.NewSaga(recorder)
sagaID := s.ID()
```

Sub-saga IDs (e.g., commission retries spawned from a parent saga) derive deterministically:

```go
func (s *Saga) NewSubSaga(kind string) *Saga {
    h := sha256.Sum256([]byte(s.id + ":" + kind))
    childID := hex.EncodeToString(h[:16])  // 32 chars, well under 36 limit
    return newSagaWithID(childID, s.recorder)
}
```

This eliminates the truncation bug class entirely — sub-saga IDs cannot exceed 36 chars by construction.

### Crossbank phase → step mapping

`crossbank_accept_saga.go`:
| Old phase | New steps |
|---|---|
| `PhaseReserveBuyerFunds` | `StepReserveBuyerFunds` |
| `PhaseReserveSellerShares` | `StepReserveSellerShares` (with `Pivot: true`) |
| `PhaseTransferFunds` | `StepDebitBuyer`, `StepCreditSeller` (Pivot on last) |
| `PhaseTransferOwnership` | `StepTransferOwnership` |
| `PhaseFinalize` | `StepFinalizeAccept` |

`crossbank_exercise_saga.go`:
| Old phase | New steps |
|---|---|
| Phase 1 | `StepDebitStrike` |
| Phase 2 | `StepCreditStrike` (Pivot) |
| Phase 3 | `StepDeliverShares` |

`crossbank_expire_saga.go`:
| Old phase | New steps |
|---|---|
| Phase 1 | `StepRefundReservation` |
| Phase 2 | `StepMarkExpired` |

(Three-phase variants — exact step decomposition refined during implementation; mapping is illustrative.)

### Files DELETED

- `stock-service/internal/service/saga_helper.go`
- `stock-service/internal/service/saga_helper_test.go`
- `stock-service/internal/service/crossbank_saga_executor.go`
- All `Phase*` constants and the phase-based recovery scaffolding.
- Any `uuid.NewString()` call that was minting saga IDs in caller code.

### Files MODIFIED

- `stock-service/internal/service/crossbank_accept_saga.go`
- `stock-service/internal/service/crossbank_exercise_saga.go`
- `stock-service/internal/service/crossbank_expire_saga.go`
- `stock-service/internal/service/saga_recovery.go` — switch becomes `StepKind`-typed with panicking default.
- `contract/shared/saga.go` — `Step.Name` becomes `StepKind`; `NewSaga` no longer accepts ID parameter; add `NewSubSaga(kind)`.

### Files ADDED

- `contract/shared/saga/steps.go` — the `StepKind` enum.
- `stock-service/internal/saga/crossbank_recorder.go` — `CrossBankRecorder` writing to `inter_bank_saga_logs`. Sits alongside the existing `recorder.go` (LocalRecorder).

## Saga Ledger (durability mechanism — IN scope, stays as-is)

There are **two** ledger tables in `stock-service`, both retained:

1. **`saga_logs`** — regular intra-bank sagas. Keyed on `(saga_id, step_name)`. `pending` / `completed` / `failed` / `compensating` / `compensated` status. Used by OTC + fund + placement sagas (already on `shared.Saga`).

2. **`inter_bank_saga_logs`** — cross-bank sagas (`stock-service/internal/model/inter_bank_saga_log.go:34`). Keyed on `(tx_id, phase, role)` — note `role` distinguishes which side of the two-party transaction we're playing (initiator vs responder). Used today by `CrossbankSagaExecutor`.

Both tables stay. The `shared.Saga.Recorder` interface gains a second implementation:

- `LocalRecorder` — writes to `saga_logs`. Used by intra-bank sagas.
- `CrossBankRecorder` — writes to `inter_bank_saga_logs`. Adds the `role` column to its row writes (carried as a field on the recorder, not the step). Used by crossbank sagas.

`StepKind` constants for crossbank steps map to the existing `phase` column. The `role` column persists as today (set when the recorder is constructed: initiator vs responder). No schema change required for the ledger tables; we just stop using the `BeginPhase`/`CompletePhase` API in favor of `Recorder` calls driven by `shared.Saga`.

Every step writes its row inside the same DB transaction as the business action. The `step_name` / `phase` column type is documented as a `StepKind` constant (string at the DB level, but enforced at the service level).

## Test Plan

### Unit tests

- Every crossbank saga happy path runs to completion under `shared.Saga` and writes the correct ledger rows.
- Each crossbank saga's compensation triggers correctly when each forward step fails (table test, one row per failure point).
- Recovery switch panics on an unregistered `StepKind` (proves the safety net works — use a test step kind that is NOT in `allSteps`).
- `MustStep` panics at construction time on an unknown kind.
- `NewSubSaga(kind)` produces deterministic ≤36-char IDs.

### Integration tests

- Existing `test-app/workflows/wf_crossbank_accept_test.go`, `wf_crossbank_exercise_test.go`, `wf_crossbank_expire_test.go` continue to pass (no behavior change).
- Restart-resume integration: kill stock-service mid-saga (between two ledger writes); on restart, recovery completes correctly using the typed switch.

## Out of Scope

- Saga step Prometheus metrics (future-ideas backlog).
- Distributed saga coordination across services — see Spec B2.
- Saga DSL or visual designer.
- Choreography saga style (orchestration stays).
