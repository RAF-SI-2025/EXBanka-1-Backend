# SAGA.pdf SG-* conformance suite

End-to-end tests that verify our **OTC option-exercise saga** against the
scenarios in `docs/bank-requirements/SAGA.pdf`, run against a real Docker stack.

> **The saga is single-bank by design.** SAGA.pdf describes a five-phase
> exercise saga over *one* ledger — the buyer's funds, the seller's shares and
> the contract all live in the same bank, and its invariants (I1 "per-currency
> SUM(available+reserved) unchanged", I2 "per-symbol SUM(quantity+reserved)
> unchanged") are single-ledger sums. The suite therefore runs against one full
> stack. (A *cross-bank* exercise — buyer in bank1, writer in bank2 — is a
> different saga, `crossbank_exercise_saga.go`, settled over SI-TX.)

## Phase → step mapping

Our exercise saga (`stock-service/internal/service/otc_exercise_saga.go`) has
more, finer steps than the spec's five phases, and it reserves the seller's
shares at **accept** time rather than at exercise. The observable guarantees and
invariants are what the suite asserts, not the internal step list.

| SAGA.pdf phase | our step(s) |
|---|---|
| F1 reserve buyer funds | `reserve_strike` |
| F2 reserve seller shares | (done at accept; consumed below) |
| F3 transfer funds buyer→seller | `settle_strike_buyer` + `credit_strike_seller` |
| F4 transfer ownership seller→buyer | `consume_seller_holding` + `upsert_buyer_holding` |
| F5 finalize / consume the contract | `mark_contract_exercised` |

## How to run

The fault scenarios need the `X-Saga-*` headers to actually inject faults, which
only happens when **api-gateway and stock-service are built with `-tags
sagafaults`** (and stock-service is started with `SAGA_FAULTS_OK=1`). The
`docker-compose.sagafaults.yml` override does exactly that; production images
never contain the fault hooks (the build tag dead-code-eliminates them).

```bash
# 1. bring up the normal stack
make docker-up                      # or: docker compose up -d

# 2. build+run the SG suite (rebuilds the two images with -tags sagafaults)
scripts/run-saga-sg.sh

# …or manually:
docker compose -f docker-compose.yml -f docker-compose.sagafaults.yml up -d --build api-gateway stock-service
cd test-app && SAGA_SG=1 go test ./workflows/ -tags integration -run '^TestSG' -v -timeout 1200s
```

`SAGA_SG=1` is required — without it the suite skips (the fault image must be
running). The infra tests (`SG-09`, `SG-11`) orchestrate containers via `docker
kill`/`docker start`; override the container names with `SG_ACCOUNT_CONTAINER` /
`SG_STOCK_CONTAINER` if your compose project name differs from the default
`exbanka-1-backend-…-1`.

The override also raises the gateway rate limits (`RATE_LIMIT_LOGIN_PER_5MIN`
etc.) — the suite logs in many times across its dozen scenarios and would
otherwise hit the default 20-logins/5-min limit and 429 mid-run.

## Scenario coverage

| # | Scenario | How it's tested | Test |
|---|---|---|---|
| SG-01 | Happy path | clean exercise → 201, contract EXERCISED | `TestSG01_HappyPath` |
| SG-02a | Caller is not the buyer | foreign JWT → 4xx, no log | `TestSG02a_NonBuyerRejected` |
| SG-02b | Contract does not exist | unknown id → 404 | `TestSG02b_UnknownContract` |
| SG-02c | Contract not "active" | exercise an already-EXERCISED contract → 4xx | `TestSG02c_AlreadyExercisedRejected` |
| SG-03 | F1 fails (no funds) | force-fail `reserve_strike` → Compensated, contract ACTIVE, clean retry succeeds | `TestSG03_…` |
| SG-04 | Share leg fails (F2) | force-fail `consume_seller_holding` → money rolls back, clean retry succeeds | `TestSG04_…` |
| SG-05 | F3 fails → C2,C1 | force-fail `credit_strike_seller` → full compensation | `TestSG05_…` |
| SG-06 | F4 fails → C3,C2,C1 | force-fail `upsert_buyer_holding` → shares & funds restored | `TestSG06_…` |
| SG-07 | F5 fails → C4..C1 | force-fail `mark_contract_exercised` → full compensation, contract ACTIVE | `TestSG07_…` |
| SG-08 | Compensator fails once, then succeeds | force-fail F3 + compensate-fail `settle_strike_buyer` once → buyer debit stuck → the saga-recovery reconciler refunds the **bound account** to its exact pre-exercise balance | `TestSG08_…` |
| SG-09 | Infrastructure down on F1 | `docker kill account-service` → `reserve_strike` fails Unavailable → Compensated, contract ACTIVE; restore → clean retry | `TestSG09_…` |
| SG-10 | Service paused mid-saga | **covered by composition** — the mid-saga-failure-then-compensate path (SG-05/06), the infra-down compensation (SG-09), and the recovery-completes-stuck-compensation path (SG-08) together exercise it; an injected delay window is shown in SG-11 | — |
| SG-11 | Coordinator killed mid-flight | inject a delay inside `settle_strike_buyer`, `docker kill stock-service` mid-saga, restart → the recovery reconciler drives the stranded saga to a terminal state (EXERCISED *or* ACTIVE both valid) with no hanging reservations | `TestSG11_…` |

Every fault test proves the spec's all-or-nothing guarantee (invariants I1/I2/I3/I6)
end-to-end: a forced-fail exercise must leave the contract ACTIVE and a
subsequent clean exercise of the **same** contract must succeed — only possible
if the seller's shares, both parties' funds, and the contract status were fully
restored.

## Notes

- **Recovery cadence.** The exercise saga has no inline compensator retry; a
  transiently-failing compensator is finished by the `saga-recovery` reconciler
  (60s tick, picks up `pending`/`compensating` rows ≥30s old, re-driving the
  whole saga under the same id). SG-08 and SG-11 drive that reconciler — they
  nudge it via `POST /api/v3/admin/crons/stock-service/saga-recovery/trigger` to
  avoid waiting a full tick.
- **Direction on recovery** (`RecoverExerciseSaga`): if the stranded saga had
  begun rolling back it is *compensated* (contract left ACTIVE); if it crashed
  mid-forward it is *resumed forward* (contract EXERCISED). Both are clean
  terminal states.
- The underlying fault-injection executor is also unit-tested in
  `contract/shared/saga/faults_test.go` (compensate-fail-N-times, before/after
  kinds, inject-delay).
