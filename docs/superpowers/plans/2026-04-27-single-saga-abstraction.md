# Single Saga Abstraction — Implementation Plan (Spec B1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `SagaExecutor` and `CrossbankSagaExecutor` in `stock-service` with a single saga abstraction (`shared.Saga`) using a typed `StepKind` enum. Recovery becomes exhaustiveness-checked via a panicking `default`. Saga IDs are minted by the runner. Two ledger tables stay (`saga_logs`, `inter_bank_saga_logs`); a second `Recorder` implementation handles the cross-bank ledger.

**Architecture:** `shared.Saga` already exists (`contract/shared/saga.go`). Add a typed `StepKind` enum in `contract/shared/saga/steps.go`. Refactor crossbank sagas (3 files) so each phase becomes 1–N steps with `Pivot: true` at compensation boundaries. Provide two recorders: `LocalRecorder` (writes `saga_logs`) and `CrossBankRecorder` (writes `inter_bank_saga_logs`, carries a `role` field set at construction). Recovery switch panics on unknown step kinds.

**Tech Stack:** Go, gorm, existing `contract/shared/saga.go` runtime.

**Spec reference:** `docs/superpowers/specs/2026-04-27-single-saga-abstraction-design.md`

---

## File Structure

**New files:**
- `contract/shared/saga/steps.go` — `StepKind` enum + `MustStep` registry helper
- `contract/shared/saga/steps_test.go`
- `stock-service/internal/saga/crossbank_recorder.go` — `CrossBankRecorder`
- `stock-service/internal/saga/crossbank_recorder_test.go`

**Modified files:**
- `contract/shared/saga.go` — `Step.Name` becomes `StepKind`; `NewSaga` no longer accepts ID; add `NewSubSaga(kind)`
- `stock-service/internal/service/saga_recovery.go` — switch becomes typed with panicking `default`
- `stock-service/internal/service/crossbank_accept_saga.go` — rewritten on `shared.Saga`
- `stock-service/internal/service/crossbank_exercise_saga.go` — rewritten on `shared.Saga`
- `stock-service/internal/service/crossbank_expire_saga.go` — rewritten on `shared.Saga`
- All callers of `NewSaga(id, ...)` — drop the id parameter
- `stock-service/internal/saga/recorder.go` — minor: `LocalRecorder` writes `StepKind` column

**Deleted files:**
- `stock-service/internal/service/saga_helper.go`
- `stock-service/internal/service/saga_helper_test.go`
- `stock-service/internal/service/crossbank_saga_executor.go`
- `stock-service/internal/model/` constants `Phase*` (move to StepKind)

---

## Task 1: Define the typed StepKind enum

**Files:**
- Create: `contract/shared/saga/steps.go`
- Test: `contract/shared/saga/steps_test.go`

- [ ] **Step 1: Write the failing test**

```go
// contract/shared/saga/steps_test.go
package saga_test

import (
	"strings"
	"testing"

	"contract/shared/saga"
)

func TestMustStep_KnownKindReturnsItself(t *testing.T) {
	got := saga.MustStep(saga.StepReserveBuyerFunds)
	if got != saga.StepReserveBuyerFunds {
		t.Errorf("got %q, want %q", got, saga.StepReserveBuyerFunds)
	}
}

func TestMustStep_UnknownKindPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unknown StepKind")
		}
		if !strings.Contains(toString(r), "unknown StepKind") {
			t.Errorf("panic message: %v", r)
		}
	}()
	_ = saga.MustStep(saga.StepKind("totally_made_up"))
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok { return s }
	if e, ok := v.(error); ok { return e.Error() }
	return ""
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/saga/...`
Expected: FAIL — package or constants missing.

- [ ] **Step 3: Implement the enum**

```go
// contract/shared/saga/steps.go
package saga

import "fmt"

// StepKind is the typed name of a saga step. Switch statements that branch on
// StepKind in recovery code MUST have a default case that panics — Go's type
// system does not enforce switch exhaustiveness, so we use the panicking default
// as the safety net.
type StepKind string

const (
	// Crossbank accept (5 steps, derived from old phases).
	StepReserveBuyerFunds   StepKind = "reserve_buyer_funds"
	StepCreateContract      StepKind = "create_contract"
	StepReserveSellerShares StepKind = "reserve_seller_shares"
	StepDebitBuyer          StepKind = "debit_buyer"
	StepCreditSeller        StepKind = "credit_seller"
	StepTransferOwnership   StepKind = "transfer_ownership"
	StepFinalizeAccept      StepKind = "finalize_accept"

	// Crossbank exercise.
	StepDebitStrike   StepKind = "debit_strike"
	StepCreditStrike  StepKind = "credit_strike"
	StepDeliverShares StepKind = "deliver_shares"

	// Crossbank expire.
	StepRefundReservation StepKind = "refund_reservation"
	StepMarkExpired       StepKind = "mark_expired"

	// OTC + Fund (already on shared.Saga; listed for completeness).
	StepReserveAndContract   StepKind = "reserve_and_contract"
	StepReservePremium       StepKind = "reserve_premium"
	StepReserveStrike        StepKind = "reserve_strike"
	StepSettlePremiumBuyer   StepKind = "settle_premium_buyer"
	StepSettleStrikeBuyer    StepKind = "settle_strike_buyer"
	StepConsumeSellerHolding StepKind = "consume_seller_holding"
	StepDebitSource          StepKind = "debit_source"
	StepCreditTarget         StepKind = "credit_target"
	StepDebitFund            StepKind = "debit_fund"
	StepCreditFund           StepKind = "credit_fund"
	StepCreditPremiumSeller  StepKind = "credit_premium_seller"
	StepCreditStrikeSeller   StepKind = "credit_strike_seller"
	StepUpsertPosition       StepKind = "upsert_position"
)

var allSteps = map[StepKind]struct{}{
	StepReserveBuyerFunds:   {}, StepCreateContract:    {}, StepReserveSellerShares: {},
	StepDebitBuyer:          {}, StepCreditSeller:      {}, StepTransferOwnership:  {},
	StepFinalizeAccept:      {}, StepDebitStrike:       {}, StepCreditStrike:       {},
	StepDeliverShares:       {}, StepRefundReservation: {}, StepMarkExpired:        {},
	StepReserveAndContract:  {}, StepReservePremium:    {}, StepReserveStrike:      {},
	StepSettlePremiumBuyer:  {}, StepSettleStrikeBuyer: {}, StepConsumeSellerHolding: {},
	StepDebitSource:         {}, StepCreditTarget:      {}, StepDebitFund:          {},
	StepCreditFund:          {}, StepCreditPremiumSeller: {}, StepCreditStrikeSeller: {},
	StepUpsertPosition:      {},
}

// MustStep returns k if k is a registered StepKind. Otherwise it panics.
// Use at saga construction: saga.AddStep(saga.MustStep(saga.StepDebitBuyer), ...)
// to fail fast on typos.
func MustStep(k StepKind) StepKind {
	if _, ok := allSteps[k]; !ok {
		panic(fmt.Sprintf("unknown StepKind: %q", k))
	}
	return k
}

// IsRegistered returns true iff k is a known StepKind.
func IsRegistered(k StepKind) bool {
	_, ok := allSteps[k]
	return ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./shared/saga/... -v`
Expected: PASS, both tests.

- [ ] **Step 5: Commit**

```bash
git add contract/shared/saga/
git commit -m "feat(saga): typed StepKind enum with MustStep panicking helper"
```

---

## Task 2: Update shared.Saga to use StepKind and mint IDs

**Files:**
- Modify: `contract/shared/saga.go`
- Modify: `contract/shared/saga_test.go` (or wherever the existing tests live)

- [ ] **Step 1: Write a failing test for the new API**

```go
// contract/shared/saga_test.go (additions)
func TestNewSaga_MintsItsOwnID(t *testing.T) {
	s := saga.NewSaga(noopRecorder{})
	if s.ID() == "" {
		t.Error("expected NewSaga to mint a non-empty ID")
	}
	other := saga.NewSaga(noopRecorder{})
	if s.ID() == other.ID() {
		t.Error("expected unique IDs across calls")
	}
}

func TestNewSubSaga_DeterministicID(t *testing.T) {
	parent := saga.NewSagaWithID("parent-id", noopRecorder{})
	a := parent.NewSubSaga("commission")
	b := parent.NewSagaWithID("parent-id", noopRecorder{}).NewSubSaga("commission")
	if a.ID() != b.ID() {
		t.Errorf("sub-saga IDs should be deterministic: %s vs %s", a.ID(), b.ID())
	}
	if len(a.ID()) > 36 {
		t.Errorf("sub-saga ID exceeds 36-char ledger limit: %d chars", len(a.ID()))
	}
}

func TestStep_NameIsStepKind(t *testing.T) {
	s := saga.NewSaga(noopRecorder{})
	s.AddStep(saga.Step{Name: saga.StepDebitBuyer, Forward: noop})
	steps := s.Steps()
	if steps[0].Name != saga.StepDebitBuyer {
		t.Errorf("Step.Name should be StepKind, got %T", steps[0].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/...`
Expected: FAIL — `NewSaga(recorder)` signature mismatch (currently takes id string).

- [ ] **Step 3: Modify `contract/shared/saga.go`**

Replace `Step.Name string` with `Step.Name StepKind`. Change `NewSaga(id string, r Recorder) *Saga` to `NewSaga(r Recorder) *Saga`. Internal generation: `id := uuid.NewString()`. Add `NewSagaWithID(id string, r Recorder) *Saga` for tests/recovery to construct with a known ID. Add `NewSubSaga(kind string) *Saga`:

```go
func (s *Saga) NewSubSaga(kind string) *Saga {
	h := sha256.Sum256([]byte(s.id + ":" + kind))
	childID := hex.EncodeToString(h[:16]) // 32 chars; fits 36-char DB column.
	return NewSagaWithID(childID, s.recorder)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./shared/... -v -run TestNewSaga`
Expected: PASS.

- [ ] **Step 5: Update all callers**

Run to find them: `grep -rn "saga\.NewSaga\|shared\.NewSaga" --include="*.go"`
For each call site (in `stock-service`):
- Replace `saga.NewSaga(uuid.NewString(), rec)` with `saga.NewSaga(rec)`.
- Where the caller stored the externally-minted ID, read it back via `s.ID()` after construction.
- Delete unused `uuid.NewString()` imports.

- [ ] **Step 6: Run all stock-service tests**

Run: `cd stock-service && go test ./...`
Expected: PASS (or fail in saga construction — fix any straggler call sites).

- [ ] **Step 7: Commit**

```bash
git add contract/shared/saga.go contract/shared/saga_test.go stock-service/internal/service/*.go
git commit -m "refactor(saga): runner mints IDs; Step.Name is typed StepKind; add NewSubSaga"
```

---

## Task 3: Add CrossBankRecorder writing to inter_bank_saga_logs

**Files:**
- Create: `stock-service/internal/saga/crossbank_recorder.go`
- Test: `stock-service/internal/saga/crossbank_recorder_test.go`

- [ ] **Step 1: Write the failing test**

```go
// stock-service/internal/saga/crossbank_recorder_test.go
package saga_test

import (
	"context"
	"testing"

	"stock-service/internal/model"
	"stock-service/internal/saga"
	// ... existing test scaffolding for in-memory db
)

func TestCrossBankRecorder_RecordsRoleAndPhase(t *testing.T) {
	db := newTestDB(t)
	repo := newInterBankRepo(db)
	rec := saga.NewCrossBankRecorder(repo, "initiator")

	err := rec.RecordForward(context.Background(), "tx-1", "reserve_buyer_funds", []byte("payload"))
	if err != nil { t.Fatal(err) }

	got, err := repo.Get("tx-1", "reserve_buyer_funds", "initiator")
	if err != nil { t.Fatal(err) }
	if got.Status != model.SagaStatusPending {
		t.Errorf("expected pending, got %s", got.Status)
	}
}

func TestCrossBankRecorder_MarkCompletedFlipsStatus(t *testing.T) {
	db := newTestDB(t)
	repo := newInterBankRepo(db)
	rec := saga.NewCrossBankRecorder(repo, "responder")

	_ = rec.RecordForward(context.Background(), "tx-2", "transfer_funds", nil)
	if err := rec.MarkCompleted(context.Background(), "tx-2", "transfer_funds"); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.Get("tx-2", "transfer_funds", "responder")
	if got.Status != model.SagaStatusCompleted {
		t.Errorf("expected completed, got %s", got.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd stock-service && go test ./internal/saga/... -run TestCrossBankRecorder`
Expected: FAIL — `NewCrossBankRecorder` does not exist.

- [ ] **Step 3: Implement the recorder**

```go
// stock-service/internal/saga/crossbank_recorder.go
package saga

import (
	"context"

	"contract/shared/saga"
	"stock-service/internal/model"
	"stock-service/internal/repository"
)

// CrossBankRecorder writes saga step ledger rows to the inter_bank_saga_logs
// table. It carries a fixed role (initiator | responder) — the cross-bank
// ledger keys on (tx_id, phase, role) so two parties can log against the same
// tx_id without conflict.
type CrossBankRecorder struct {
	repo *repository.InterBankSagaLogRepository
	role string
}

func NewCrossBankRecorder(repo *repository.InterBankSagaLogRepository, role string) *CrossBankRecorder {
	if role != "initiator" && role != "responder" {
		panic("CrossBankRecorder role must be initiator or responder, got " + role)
	}
	return &CrossBankRecorder{repo: repo, role: role}
}

func (r *CrossBankRecorder) RecordForward(ctx context.Context, sagaID string, step saga.StepKind, payload []byte) error {
	return r.repo.UpsertByTxPhaseRole(&model.InterBankSagaLog{
		TxID:    sagaID,
		Phase:   string(step),
		Role:    r.role,
		Status:  model.SagaStatusPending,
		Payload: payload,
	})
}

func (r *CrossBankRecorder) MarkCompleted(ctx context.Context, sagaID string, step saga.StepKind) error {
	row, err := r.repo.Get(sagaID, string(step), r.role)
	if err != nil { return err }
	row.Status = model.SagaStatusCompleted
	return r.repo.Save(row)
}

func (r *CrossBankRecorder) MarkFailed(ctx context.Context, sagaID string, step saga.StepKind, reason string) error {
	row, err := r.repo.Get(sagaID, string(step), r.role)
	if err != nil { return err }
	row.Status = model.SagaStatusFailed
	row.LastError = reason
	return r.repo.Save(row)
}

func (r *CrossBankRecorder) RecordCompensation(ctx context.Context, sagaID string, step saga.StepKind) error {
	return r.repo.UpsertByTxPhaseRole(&model.InterBankSagaLog{
		TxID:   sagaID,
		Phase:  "compensate_" + string(step),
		Role:   r.role,
		Status: model.SagaStatusCompensating,
	})
}

func (r *CrossBankRecorder) MarkCompensated(ctx context.Context, sagaID string, step saga.StepKind) error {
	row, err := r.repo.Get(sagaID, "compensate_"+string(step), r.role)
	if err != nil { return err }
	row.Status = model.SagaStatusCompensated
	return r.repo.Save(row)
}

func (r *CrossBankRecorder) IsCompleted(ctx context.Context, sagaID string, step saga.StepKind) (bool, error) {
	row, err := r.repo.Get(sagaID, string(step), r.role)
	if err != nil { return false, nil }
	return row.Status == model.SagaStatusCompleted, nil
}
```

(If `Save` is not on the repo, add a small `Save(row *InterBankSagaLog) error` method calling `db.Save(row).Error`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd stock-service && go test ./internal/saga/... -run TestCrossBankRecorder -v`
Expected: PASS, both tests.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/saga/crossbank_recorder.go \
        stock-service/internal/saga/crossbank_recorder_test.go \
        stock-service/internal/repository/inter_bank_saga_log_repository.go
git commit -m "feat(saga): CrossBankRecorder writes inter_bank_saga_logs via shared.Saga interface"
```

---

## Task 4: Rewrite crossbank_accept_saga onto shared.Saga

**Files:**
- Modify: `stock-service/internal/service/crossbank_accept_saga.go`
- Modify: `stock-service/internal/service/crossbank_accept_saga_test.go` (or create)

- [ ] **Step 1: Capture current behavior in tests (characterization)**

Run the existing crossbank-accept tests; confirm they pass on current code. These are the regression net for the rewrite.

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankAccept -v
```

Expected: PASS on current code.

- [ ] **Step 2: Rewrite using shared.Saga**

```go
// stock-service/internal/service/crossbank_accept_saga.go
package service

import (
	"context"

	cb "contract/shared/saga"
	stocksaga "stock-service/internal/saga"
)

func (s *CrossbankAcceptSaga) Run(ctx context.Context, in CrossbankAcceptInput) error {
	rec := stocksaga.NewCrossBankRecorder(s.logsRepo, "initiator")
	saga := cb.NewSaga(rec)

	saga.Add(cb.Step{
		Name:    cb.MustStep(cb.StepReserveBuyerFunds),
		Forward: func(ctx context.Context, st *cb.State) error {
			return s.reserveBuyerFunds(ctx, in)
		},
		Backward: func(ctx context.Context, st *cb.State) error {
			return s.releaseBuyerFunds(ctx, in)
		},
	})
	saga.Add(cb.Step{
		Name:    cb.MustStep(cb.StepCreateContract),
		Forward: func(ctx context.Context, st *cb.State) error {
			cid, err := s.createContract(ctx, in)
			st.Set("contract_id", cid)
			return err
		},
		Backward: func(ctx context.Context, st *cb.State) error {
			cid := st.GetUint64("contract_id")
			return s.deleteContract(ctx, cid)
		},
	})
	saga.Add(cb.Step{
		Name:  cb.MustStep(cb.StepReserveSellerShares),
		Pivot: true,  // compensation boundary
		Forward: func(ctx context.Context, st *cb.State) error {
			return s.reserveSellerShares(ctx, in, st.GetUint64("contract_id"))
		},
		Backward: func(ctx context.Context, st *cb.State) error {
			return s.releaseSellerShares(ctx, in)
		},
	})
	saga.Add(cb.Step{
		Name:    cb.MustStep(cb.StepDebitBuyer),
		Forward: func(ctx context.Context, st *cb.State) error {
			return s.debitBuyer(ctx, in, saga.ID())
		},
	})
	saga.Add(cb.Step{
		Name:    cb.MustStep(cb.StepCreditSeller),
		Forward: func(ctx context.Context, st *cb.State) error {
			return s.creditSeller(ctx, in, saga.ID())
		},
	})
	saga.Add(cb.Step{
		Name:    cb.MustStep(cb.StepTransferOwnership),
		Forward: func(ctx context.Context, st *cb.State) error {
			return s.transferOwnership(ctx, in, st.GetUint64("contract_id"))
		},
	})
	saga.Add(cb.Step{
		Name:    cb.MustStep(cb.StepFinalizeAccept),
		Forward: func(ctx context.Context, st *cb.State) error {
			return s.finalize(ctx, in, st.GetUint64("contract_id"))
		},
	})

	return saga.Execute(ctx)
}
```

The existing helper methods (`s.reserveBuyerFunds`, `s.createContract`, etc.) remain — they're the per-step business logic. Only the orchestration changes.

- [ ] **Step 3: Run the existing crossbank-accept tests**

Run: `cd stock-service && go test ./internal/service/... -run TestCrossbankAccept -v`
Expected: PASS — behavior is unchanged.

- [ ] **Step 4: Add a compensation test for each forward step**

Table-driven test that fails each step in turn and asserts the prior steps were compensated:

```go
func TestCrossbankAccept_Compensation(t *testing.T) {
	cases := []struct{
		failAt cb.StepKind
		expectCompensated []cb.StepKind
	}{
		{cb.StepCreateContract,        []cb.StepKind{cb.StepReserveBuyerFunds}},
		{cb.StepReserveSellerShares,   []cb.StepKind{cb.StepCreateContract, cb.StepReserveBuyerFunds}},
		// Steps after the Pivot do NOT trigger backward of pre-Pivot steps.
		{cb.StepDebitBuyer,            nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.failAt), func(t *testing.T) {
			// ... inject a failing handler at tc.failAt; run; assert compensation log
		})
	}
}
```

- [ ] **Step 5: Run compensation tests**

Run: `cd stock-service && go test ./internal/service/... -run TestCrossbankAccept_Compensation -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/service/crossbank_accept_saga.go \
        stock-service/internal/service/crossbank_accept_saga_test.go
git commit -m "refactor(stock-service): crossbank accept saga on shared.Saga with typed StepKind"
```

---

## Task 5: Rewrite crossbank_exercise_saga

**Files:**
- Modify: `stock-service/internal/service/crossbank_exercise_saga.go`
- Modify: tests

- [ ] **Step 1: Capture current behavior**

```bash
cd stock-service && go test ./internal/service/... -run TestCrossbankExercise -v
```
Expected: PASS on current code.

- [ ] **Step 2: Rewrite using shared.Saga**

Same shape as Task 4. The 3 phases map to:
- `StepDebitStrike` (with Backward to refund)
- `StepCreditStrike` (Pivot: true)
- `StepDeliverShares` (no Backward — past the pivot)

- [ ] **Step 3: Run tests**

Run: `cd stock-service && go test ./internal/service/... -run TestCrossbankExercise -v`
Expected: PASS.

- [ ] **Step 4: Add compensation test**

Same shape as Task 4 step 4.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/crossbank_exercise_saga.go \
        stock-service/internal/service/crossbank_exercise_saga_test.go
git commit -m "refactor(stock-service): crossbank exercise saga on shared.Saga"
```

---

## Task 6: Rewrite crossbank_expire_saga

**Files:**
- Modify: `stock-service/internal/service/crossbank_expire_saga.go`

- [ ] **Step 1-5: Same pattern as Task 5**

Steps:
- `StepRefundReservation` (with Backward to re-reserve)
- `StepMarkExpired` (Pivot)

Commit: `refactor(stock-service): crossbank expire saga on shared.Saga`

---

## Task 7: Rewrite saga_recovery.go switch with panicking default

**Files:**
- Modify: `stock-service/internal/service/saga_recovery.go:145-196`

- [ ] **Step 1: Write a test that asserts the panicking default**

```go
func TestRecovery_UnknownStepKindPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown StepKind")
		}
	}()
	step := &SagaLog{StepName: "totally_made_up"}
	reconcileStep(step)
}
```

- [ ] **Step 2: Modify the switch**

```go
// stock-service/internal/service/saga_recovery.go (around line 145)

func reconcileStep(step *SagaLog) error {
	switch saga.StepKind(step.StepName) {
	case saga.StepReserveBuyerFunds, saga.StepCreateContract:
		return reconcileReserve(step)
	case saga.StepReserveSellerShares:
		return reconcileSellerReserve(step)
	case saga.StepDebitBuyer, saga.StepCreditSeller:
		return reconcileLedger(step)
	case saga.StepTransferOwnership:
		return reconcileOwnership(step)
	case saga.StepFinalizeAccept:
		return reconcileFinalize(step)

	case saga.StepDebitStrike, saga.StepCreditStrike, saga.StepDeliverShares:
		return reconcileExercise(step)

	case saga.StepRefundReservation, saga.StepMarkExpired:
		return reconcileExpire(step)

	// OTC + fund step kinds (already on shared.Saga).
	case saga.StepReserveAndContract, saga.StepReservePremium, saga.StepReserveStrike:
		return reconcileOTCReserve(step)
	case saga.StepSettlePremiumBuyer, saga.StepSettleStrikeBuyer, saga.StepConsumeSellerHolding:
		return reconcileOTCSettle(step)
	case saga.StepDebitSource, saga.StepCreditTarget:
		return reconcileFundFX(step)
	case saga.StepDebitFund, saga.StepCreditFund:
		return reconcileFundLedger(step)
	case saga.StepCreditPremiumSeller, saga.StepCreditStrikeSeller:
		return reconcileOTCCredit(step)
	case saga.StepUpsertPosition:
		return reconcileFundPosition(step)

	default:
		panic(fmt.Sprintf("recovery: unhandled StepKind %q — add case to switch in saga_recovery.go",
			step.StepName))
	}
}
```

(Reuse or split the per-group reconcile functions from the existing code; this is mostly a renaming pass.)

- [ ] **Step 3: Run the recovery tests**

Run: `cd stock-service && go test ./internal/service/... -run TestRecovery -v`
Expected: PASS for known kinds; panic-test PASS.

- [ ] **Step 4: Commit**

```bash
git add stock-service/internal/service/saga_recovery.go \
        stock-service/internal/service/saga_recovery_test.go
git commit -m "refactor(saga-recovery): typed StepKind switch with panicking default"
```

---

## Task 8: Delete legacy executor files

**Files:**
- Delete: `stock-service/internal/service/saga_helper.go`
- Delete: `stock-service/internal/service/saga_helper_test.go`
- Delete: `stock-service/internal/service/crossbank_saga_executor.go`

- [ ] **Step 1: Confirm no remaining callers**

```bash
grep -rn "SagaExecutor\|CrossbankSagaExecutor\|RunStep\|RunCompensation\|BeginPhase\|CompletePhase\|FailPhase" \
    --include="*.go" stock-service/
```
Expected: NO matches in non-deleted files (only matches in the files about to be deleted).

If any matches remain, fix the call sites first — they're stragglers from Tasks 4-6.

- [ ] **Step 2: Delete the files**

```bash
rm stock-service/internal/service/saga_helper.go \
   stock-service/internal/service/saga_helper_test.go \
   stock-service/internal/service/crossbank_saga_executor.go
```

- [ ] **Step 3: Remove the Phase* constants from `stock-service/internal/model/`**

```bash
grep -rn "model\.Phase" --include="*.go" stock-service/
```
Expected: NO matches. If any remain, they're stragglers.

Then delete the constants. They typically live in a single file like `stock-service/internal/model/inter_bank_saga_log.go` near the model definition. Remove the `Phase*` const block.

- [ ] **Step 4: Build and test**

Run:
```bash
cd stock-service && go build ./... && go test ./...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A stock-service/
git commit -m "chore(stock-service): delete legacy SagaExecutor and CrossbankSagaExecutor"
```

---

## Task 9: Integration test for cross-bank saga durability

**Files:**
- Create or modify: `test-app/workflows/wf_crossbank_saga_durability_test.go`

- [ ] **Step 1: Write a kill-mid-saga test**

```go
//go:build integration
// +build integration

package workflows_test

import (
	"context"
	"testing"
	"time"
)

func TestCrossbankAccept_KillMidSaga_RecoversCleanly(t *testing.T) {
	// 1. Start a crossbank accept against a peer-bank stub.
	// 2. Force the peer to delay/timeout on the transfer-ownership phase.
	// 3. Restart stock-service.
	// 4. Assert recovery completes the saga (or compensates) and ledger rows
	//    are in a terminal state (completed or compensated, never pending).
	// Test scaffold reuses peerbank stub + docker-compose helpers.
	t.Skip("requires test infra — implement after docker-compose helper for mid-test service restart lands")
}
```

(If the docker-compose mid-test restart helper doesn't exist, leave this skipped and create it as a follow-up. Existing crossbank tests already cover the happy + compensation paths — durability is the new gap.)

- [ ] **Step 2: Run existing crossbank integration tests**

Run: `make test-integration TESTS='Crossbank'`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/wf_crossbank_saga_durability_test.go
git commit -m "test(crossbank): scaffold kill-mid-saga durability test"
```

---

## Task 10: Update spec docs

- [ ] **Step 1: Update `Specification.md`**

Add to section 19 (Kafka topics) or section 21 (business rules) a brief note:
> Cross-bank sagas use the `shared.Saga` runtime with `CrossBankRecorder` writing to `inter_bank_saga_logs`. Steps are typed via `contract/shared/saga.StepKind`; recovery panics on unknown kinds.

- [ ] **Step 2: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): typed StepKind + CrossBankRecorder reference"
```

---

## Self-Review

**Spec coverage:**
- ✅ `StepKind` typed enum — Task 1
- ✅ Recovery exhaustiveness via panicking `default` — Task 7
- ✅ Saga ID minted by runner — Task 2
- ✅ Sub-saga ID derivation ≤36 chars — Task 2
- ✅ Two ledgers preserved (saga_logs + inter_bank_saga_logs) — Task 3
- ✅ Crossbank phase → step rewrite for all 3 sagas — Tasks 4, 5, 6
- ✅ Files deleted (saga_helper, crossbank_saga_executor) — Task 8

**Placeholders:** Task 9's integration test is intentionally skipped pending infra — flagged explicitly.

**Type consistency:** `StepKind` is `string`-typed in package `saga`. `Step.Name` field is `StepKind`. `MustStep` returns `StepKind`. Recorder methods take `saga.StepKind`. Consistent across tasks.

**Commit cadence:** ~10 commits, one per task.
