# SAGA Test Suite (SG-01…SG-11) — Implementation Plan

**Date:** 2026-05-29
**Source spec:** `docs/SAGA.pdf` (5-phase OTC option exercise saga + 11 fault scenarios)
**Status:** Planned (not yet implemented)

## Context & decisions

The spec describes a 5-phase exercise saga (F1 reserve buyer funds → F2 reserve seller
securities → F3 transfer funds → F4 transfer ownership → F5 invalidate contract), each phase
with an inverse compensator (C1…C5) run in reverse, idempotent, retried to success. It defines
fault-injection headers (`X-Saga-Force-Fail`, `-Force-Fail-Kind`, `-Compensate-Fail`,
`-Compensate-Fail-Times`, `-Inject-Delay`) and chaos mechanisms (Toxiproxy, `docker compose
pause`, `docker compose kill`). Eleven scenarios SG-01…SG-11.

Decisions taken during brainstorming (2026-05-29):
- **Scope: all tiers (A+B+C).** A = SG-01..04 runnable today; B = forced-failure/compensator
  tests needing fault hooks (SG-05..08); C = infra-chaos (SG-09..11).
- **Remove the pivot** in `stock-service/internal/service/otc_exercise_saga.go` so post-`F4`
  failures fully compensate, matching the PDF's symmetric C5…C1. Differing step *count* is
  acceptable, but compensation must be total (no irreversible pivot under normal faults).
- The SG tests drive the **OTC option exercise** flow. The local intra-bank exercise saga is the
  primary target; the cross-bank (SI-TX) path reuses the same phases but must NOT have its
  frozen interbank routes refactored (see `feedback_interbank_protocol_frozen`).

### Reality gaps the plan must close (verified in code)
- No fault-injection hooks exist anywhere (`X-Saga-*` absent repo-wide).
- No Toxiproxy in docker-compose / Makefile.
- Current exercise saga is 9 steps with `Pivot:true` on `consume_seller_holding`; saga log is
  one row per step (`stock-service/internal/model/saga_log.go`), statuses
  `pending|completed|failed|compensating|compensated|dead_letter` — a superset of the PDF's
  `Running|Completed|Compensating|Compensated`.
- Crash-recovery exists (`contract/shared/saga/saga_recovery.go` RecoveryRunner, 30s tick) — SG-11
  leans on this; verify it resumes/*compensates* exercise sagas, not just orders.

## Phases

### Phase 0 — Pivot removal & compensation symmetry (backend correctness first)
Goal: a forced failure at *any* phase fully restores prior state (invariants I1–I3, I6).
1. In `otc_exercise_saga.go`, reorder so all money movements (reserve, settle, credit seller)
   and the holding move are individually compensatable; drop `Pivot:true`. The holding transfer
   (`consume_seller_holding` + `upsert_buyer_holding`) must have working Backward steps
   (return shares to seller / remove from buyer) instead of being treated as irreversible.
2. Ensure each compensator is idempotent and keyed by `(saga_id, step)` so retries are safe.
3. Tests: extend `otc_exercise_saga_guards_test.go` + add a saga-level unit test asserting that
   forcing each forward step to fail leaves balances/holdings/contract identical to start
   (mirrors SG-05..07 at unit level, no HTTP).
4. Verify invariants helper: add `assertSagaInvariants` checking per-currency
   `SUM(available+reserved)` constant, per-symbol `SUM(quantity+reserved)` constant, all
   `reserved_*`=0 at terminal, contract not-valid iff Completed.

### Phase 1 — Fault-injection hooks (test-build gated)
Goal: deterministically fail/delay a named phase or compensator from a request header.
1. Add a `saga.FaultSpec` carried on `context.Context`, parsed from `X-Saga-*` headers.
2. In `contract/shared/saga` executor, before/after each Forward and each Backward, consult the
   FaultSpec: force-fail (before|after side effects), fail-compensator N times then succeed,
   inject delay.
3. **Release-mode guard:** the hook is compiled only under a build tag (`saga_faults`) OR gated
   by env `SAGA_FAULTS_ENABLED`; the service refuses to start if the flag is on without the
   build tag, so production can never inject faults. Document in CLAUDE.md.
4. Plumb headers gateway→gRPC: gateway copies `X-Saga-*` request headers into outgoing gRPC
   metadata (only when the build/env gate is on); stock-service interceptor reads metadata into
   the FaultSpec on the context.
5. Tests: unit-test the FaultSpec parser and the executor hook points.

### Phase 2 — Saga log assertions & HTTP exposure
1. Ensure the exercise response returns `saga_id` so a test can poll the log.
2. Add (test-only or admin) read of the saga log by `saga_id`; map per-step rows → the PDF's
   `status`/`current_step`/`log[]` shape in the assertion helper (no schema change needed).
3. Poll-to-terminal helper with 30s timeout (`Completed|Compensated`).

### Phase 3 — Integration harness SG-01…SG-08 (Tiers A+B)
Add `test-app/workflows/saga_exercise_test.go` (build tag `integration`). Reuse helpers
(`setupActivatedClient`, seeding, `testing_mode` for fast fills — see
`reference_local_order_testing`). One test per scenario:
- SG-01 happy path; SG-02a–d pre-saga validation (4xx, no log); SG-03 F1 fail (insufficient
  funds via seed); SG-04 F2 fail (insufficient shares via seed); SG-05 force F3; SG-06 force F4;
  SG-07 force F5; SG-08 compensator-fail-once-then-succeed.
- Each asserts: participant rows, log shape, `bank.transactions` row count, invariants I1–I6.

### Phase 4 — Chaos infra SG-09…SG-11 (Tier C)
1. Add an optional Toxiproxy service to `docker-compose.yml` (+ `-remote`) sitting between
   stock↔account (the "trading"↔"bank" link); gated behind a compose profile so normal runs are
   unaffected.
2. Test helpers to program toxics (latency, down, bandwidth=0) and to `docker compose
   pause/unpause` / `kill -s KILL` a service from Go (exec docker CLI; skip if unavailable).
3. SG-09a/b/c (pause / latency>timeout / partition on bank during F1 → Compensated,
   current_step=1, no side effects). SG-10 (pause mid-saga via inject-delay window → resume,
   Compensated). SG-11 (SIGKILL coordinator mid-flight → RecoveryRunner resumes → Completed or
   Compensated, invariants hold, no dangling reservations).
4. These are heavy/host-dependent — gate behind an env (e.g. `SAGA_CHAOS=1`) and document.

## Testing requirement
- Unit: pivot-removal compensation symmetry; FaultSpec parser; executor hook points.
- Integration: SG-01…SG-11 as above; all assert spec behaviour (balances, log, invariants), not
  just status codes.
- Run `make test` + the integration suite; `make lint` on touched services.

## Risks
- Pivot removal touches a live money saga — do Phase 0 first, behind thorough invariant tests,
  before any harness work.
- Fault hooks must be impossible to enable in production (build-tag + startup guard).
- Toxiproxy/docker-kill tests are environment-sensitive; keep them opt-in so the default
  `make test-integration` stays green in CI.

## Out of scope (tracked elsewhere)
- Cross-bank SI-TX exercise route shapes are frozen — do not refactor (only add fault hooks
  behind the gate if exercising the interbank path).
