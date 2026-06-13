# EXBanka — Comprehensive Requirements-Driven Test Plan

This directory is a complete, **agent-consumable test plan** that maps every feature,
sub-feature, and option in the five requirements documents (Celina 1–5), cross-referenced
with the two test/defense documents, to concrete, executable test cases. Each test case
carries the literal HTTP request (method, path, auth role, body), the expected outcome
(status, response fields, side-effects), and its negative siblings — so any agent or
human can run it immediately against a live local stack. Coverage is exhaustive by
construction: **every feature, every feature-inside-a-feature, every specified option,
each with at least one positive and one negative outcome.** Ordinary request/response
objects are tested for equivalent functionality; cross-bank **SI-TX protocol** objects
are pinned to the wire spec exactly.

---

## File map

| File | Responsibility |
|---|---|
| [`README.md`](./README.md) | This master index: purpose, file map, ID scheme, how-to, coverage dashboard, spec link. |
| [`00-setup-and-conventions.md`](./00-setup-and-conventions.md) | Stack/seeder bring-up, base URL & versioning, seed creds + role→token recipes, fixtures (funded account, `testing_mode`, RSD sentinel), the test-case template, fast-path verification, common assertions (error envelope, gRPC→HTTP map, balance reads, Kafka scans), functional-equivalence rule. |
| [`celina-1-user-management.md`](./celina-1-user-management.md) | Auth, login, brute-force lockout, employees, clients, RBAC, reset/activation, sessions. |
| [`celina-2-core-banking.md`](./celina-2-core-banking.md) | Accounts, companies, payments, transfers, recipients, menjačnica (exchange), cards, loans/installments. |
| [`celina-3-securities.md`](./celina-3-securities.md) | Exchanges, listings, orders, agent limits/approval, portfolio, dividends, capital-gains tax, recurring orders, watchlist, price alerts. |
| [`celina-4-otc-and-funds.md`](./celina-4-otc-and-funds.md) | OTC stocks, OTC options + SAGA exercise, option/premium tax, investment funds, bank-profit portal. |
| [`celina-5-cross-bank.md`](./celina-5-cross-bank.md) | Two-stack setup, inter-bank 2PC payments, cross-bank OTC SAGA, SI-TX protocol conformance. |
| [`cross-cutting-verification.md`](./cross-cutting-verification.md) | The full verification-challenge mechanism: positive + negative, every method. |
| [`todo-final-notifications-and-mobile.md`](./todo-final-notifications-and-mobile.md) | `TODO_final.pdf`: cross-cutting notification coverage (email + in-app + push per event), mobile-app features, and Quick Approve. |
| [`coverage-matrix.md`](./coverage-matrix.md) | Every feature/sub-feature/option → TC IDs → existing Go test → status. The single exhaustiveness checklist. |

---

## ID scheme

Test cases are identified as **`TC-C<celina>-<AREA>-<nnn>`**:

- `<celina>` — `1`–`5`. The cross-cutting files use a domain prefix instead of a celina
  number: `TC-VERIF-<nnn>` (verification), `TC-NOTIF-<nnn>` and `TC-MOBILE-<nnn>` (TODO_final).
- `<AREA>` — a short uppercase domain tag, e.g. `LOGIN`, `EMP`, `RBAC`, `PAY`, `TRF`,
  `CARD`, `LOAN`, `ORD`, `PORT`, `TAX`, `FUND`, `OTC`, `SITX`.
- `<nnn>` — a zero-padded sequence number, unique within `<celina>-<AREA>`.
- Actor variants of one logical case take suffixes **`a/b/c`** (e.g. `TC-C1-LOGIN-003a`
  = employee, `…003b` = client, `…003c` = unauthenticated).

**IDs are stable forever** — never renumber or reuse an ID. The coverage matrix and any
external pass/fail logs key on them.

The uniform **test-case template** every file uses is defined verbatim in
[`00-setup-and-conventions.md` §5](./00-setup-and-conventions.md#5-the-test-case-template--id-scheme).

---

## How an agent uses this

1. **Read [`00-setup-and-conventions.md`](./00-setup-and-conventions.md) first.** Bring
   up the stack (`make docker-up`), wait for `seeder: all bootstrapping complete`, and
   confirm `GET /api/v3/version` returns 200.
2. **Obtain the tokens you need** for the actors in scope (admin / supervisor / agent /
   client / employee-on-behalf / unauthenticated) using the login recipe in §3.
3. **Pick the Celina file** matching your task (or `cross-cutting-verification.md`) and
   **execute its TCs top-to-bottom.** Honor each TC's `Preconditions` (some chain off a
   prior TC or a fixture helper). Use the fixture, fast-path, and Kafka/balance helpers
   from §4–§7.
4. **Assert the full `Expected` block** — never status code alone for money/state changes:
   check `error.code`, response fields, balance deltas, status transitions, Kafka events,
   and ledger/audit rows.
5. **Record pass/fail against the TC ID.** A failing positive or a negative that did not
   return its documented `error.code` is a finding — log it; do not silently adjust the TC.
6. **Reconcile against [`coverage-matrix.md`](./coverage-matrix.md):** every
   feature/sub-feature/option is a row pointing at its TC IDs and any existing Go test. A
   row with no TC, or marked `NO-ENDPOINT`, is a visible coverage gap.

For protocol work (Celina 5 SI-TX), remember the functional-equivalence rule
([§8](./00-setup-and-conventions.md#8-functional-equivalence-rule-protocol-vs-everything-else)):
protocol bodies must match `contract/sitx/testdata/*.json` and the protocol spec exactly;
frozen routes are asserted as-spec'd, not "corrected."

---

## Coverage dashboard

The authoritative, per-feature breakdown lives in
[`coverage-matrix.md`](./coverage-matrix.md); the summary counts below are tallied from that
file's "Coverage rows" blocks. **Features** = number of coverage rows (each is one
feature / sub-feature / option).

| Source file | Features | Covered | Partial | NO-ENDPOINT |
|---|---:|---:|---:|---:|
| C1 — User Management | 55 | 38 | 15 | 2 |
| C2 — Core Banking | 83 | 47 | 31 | 5 |
| C3 — Securities | 82 | 69 | 6 | 7 |
| C4 — OTC & Funds | 69 | 62 | 4 | 3 |
| C5 — Cross-Bank | 71 | 50 | 15 | 6 |
| Cross-cutting — Verification | 32 | 22 | 7 | 3 |
| TODO_final — Notifications & Mobile | 43 | 20 | 17 | 6 |
| **Grand total** | **435** | **308** | **95** | **32** |

See [`coverage-matrix.md`](./coverage-matrix.md) for the full row-by-row matrix and the
gap list (every `partial` / `NO-ENDPOINT` row with a one-line note on what is missing).

---

## Verification status

Static accuracy checks run when this plan was authored (2026-06-07):

- **Link-check** — 397 unique `file.go::Func` references to existing Go tests; all resolve to
  real functions (1 fixed: `loginClient` → `loginAsClient`).
- **Path reconciliation** — 293 distinct documented request paths checked against the gateway
  router (`api-gateway/internal/router/router_v3.go`); all resolve to registered routes
  (1 fixed: order rejection is `POST /api/v3/orders/:id/reject`, not `/decline`).
- **Placeholder scan** — clean (no `TBD`/`TODO`/`FIXME`/empty sections).

**Live spot-execution (partial, 2026-06-07).** A two-bank-capable stack (`bank1`, gateway :8080,
seeded admin/agent/supervisor/clients) was brought up and used to live-verify the bug fixes
landed this session:
- **Bug C (client forex/options 403):** client→`403` on `/securities/forex` + `/securities/options`,
  still `200` on stocks/futures; agent→`200`. ✅
- **Bug B (over-limit approval gate):** agent with `need_approval=false` + a 1000 RSD limit places a
  ~4500 RSD order → `status: pending` (was auto-approved). ✅

Broader spot-execution of the remaining files (auth flows, payments/transfers, OTC, funds,
cross-bank) is still recommended before relying on the full plan; record results here as they run.

---

## Source of truth

This plan is generated from the design spec — read it for the coverage methodology,
the per-Celina scope checklist, and the accuracy/non-duplication rules:

**[`docs/superpowers/specs/2026-06-07-comprehensive-test-plan-design.md`](../superpowers/specs/2026-06-07-comprehensive-test-plan-design.md)**

Implementation plan (tasks/steps):
[`docs/superpowers/plans/2026-06-07-comprehensive-test-plan.md`](../superpowers/plans/2026-06-07-comprehensive-test-plan.md).
Endpoint/entity/enum/rule references throughout point at `docs/api/REST_API_v3.md` and
`docs/Specification.md` (§17 routes, §18 entities, §20 enums, §21 business rules,
§14 error mapping, §6 auth/roles/permissions).
