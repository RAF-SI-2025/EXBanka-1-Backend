# Comprehensive Requirements-Driven Test Plan — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:dispatching-parallel-agents to implement this plan. The five Celina tasks (Task 2–6) are independent and SHOULD be fanned out in parallel. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a complete, agent-consumable test-plan under `docs/test-plan/` that maps every feature/sub-feature/option in Celina 1–5 (+ E2E doc + defense flow) to concrete, executable test cases (method, path, auth role, body, expected status/response/side-effects) covering every positive and negative outcome.

**Architecture:** A master index + per-Celina markdown files + a cross-cutting verification file + a coverage matrix. Each test case follows a fixed template. Exact request detail is sourced from `docs/api/REST_API_v3.md`, `docs/Specification.md`, the api-gateway handlers, and existing `test-app/workflows` tests — never invented. Generation fans out one agent per Celina file; results reconcile into the coverage matrix.

**Tech Stack:** Markdown docs only. Verification uses the local docker-compose stack (`make docker-up` + seeder) and `curl`/existing Go workflow tests for spot-checks. No service code changes.

**Spec:** `docs/superpowers/specs/2026-06-07-comprehensive-test-plan-design.md` (read it first).

---

## File Structure

| File | Responsibility |
|---|---|
| `docs/test-plan/README.md` | Master index: purpose, how to use, file map, coverage dashboard, ID scheme, link to spec. |
| `docs/test-plan/00-setup-and-conventions.md` | Stack/seeder bring-up, base URL, seed creds, role→token recipes, the test-case template, fast-path verification, common assertions, balance/Kafka inspection, error-code map. |
| `docs/test-plan/celina-1-user-management.md` | Auth, login, brute-force, employees, clients, RBAC, reset/activation, sessions. |
| `docs/test-plan/celina-2-core-banking.md` | Accounts, companies, payments, transfers, recipients, menjačnica, cards, loans/installments. |
| `docs/test-plan/celina-3-securities.md` | Exchanges, listings, orders, agent limits/approval, portfolio, dividends, tax, recurring orders, watchlist, alerts. |
| `docs/test-plan/celina-4-otc-and-funds.md` | OTC stocks, OTC options + SAGA exercise, option/premium tax, investment funds, bank-profit portal. |
| `docs/test-plan/celina-5-cross-bank.md` | Two-stack setup, inter-bank 2PC payments, cross-bank OTC, SI-TX protocol conformance. |
| `docs/test-plan/cross-cutting-verification.md` | Full verification-challenge mechanism: positive + negative. |
| `docs/test-plan/coverage-matrix.md` | Every feature/sub-feature/option → TC IDs → existing Go test → status. |

---

## Task 1: Scaffold + conventions + README

**Files:**
- Create: `docs/test-plan/00-setup-and-conventions.md`
- Create: `docs/test-plan/README.md`

- [ ] **Step 1: Gather ground-truth env facts**

Read these to fill the conventions file with ACCURATE values (do not guess):
- `docs/api/REST_API_v3.md` (top section: base URL, auth header, error envelope).
- `docs/Specification.md` §6 (auth/roles/permissions), §20 (enums), §21 (business rules), §14 (error mapping).
- `test-app/workflows/helpers_test.go` (how tests log in, fund accounts, scan Kafka, run verification).
- Memory facts: seed admin password `AdminAdmin2026!.`, base path note, `testing_mode`, RSD sentinel funding, `available_balance` column.
- `docker-compose.yml` + `Makefile` (`docker-up`, `seeder-init`, `test-integration`).

- [ ] **Step 2: Write `00-setup-and-conventions.md`**

Must contain these sections with concrete content:
1. **Environment bring-up** — exact commands: `make docker-up`, seeder init, health check (`GET /api/v3/version`), how to confirm readiness.
2. **Base URL & versioning** — `http://localhost:8080`, all routes under `/api/v3`, v1/v2 → 404.
3. **Seed credentials & roles** — admin login, and how to obtain a token for each role (admin, supervisor, agent, client, employee-on-behalf). Give the literal `POST /api/v3/auth/login` request + how to read `access_token` from the response, and how to set `Authorization: Bearer <token>`.
4. **Creating fixtures** — how to create a client + funded account (cite the helper / the admin endpoints), how to fund the RSD sentinel for testing, how to flip `testing_mode` so orders fill fast.
5. **The test-case template** — copy the exact template from spec §4 and the ID scheme `TC-C<n>-<AREA>-<nnn>`.
6. **Verification fast-path** — how `verification.skip` (supervisor/admin) bypasses TOTP; note that client self-service actions need the full flow (→ cross-cutting file).
7. **Common assertions** — error envelope `{"error":{"code","message","details"}}`; the gRPC→HTTP error-code map (table from CLAUDE.md); how to check a balance (`GET` account, read `available_balance`), how to scan Kafka for `notification.send-email` (cite helper), how to read audit/ledger.
8. **Functional-equivalence rule** — non-protocol objects need equivalent functionality; protocol (SI-TX) objects must match the wire spec exactly.

- [ ] **Step 3: Write `README.md`**

Index with: one-paragraph purpose, the file map table (above), the ID scheme, "how an agent uses this" (read 00-setup, pick a Celina file, execute TCs top-to-bottom, record pass/fail against TC IDs), a coverage-dashboard placeholder that links to `coverage-matrix.md`, and a link back to the spec.

- [ ] **Step 4: Commit**

```bash
git add docs/test-plan/00-setup-and-conventions.md docs/test-plan/README.md
git commit -m "docs(test-plan): scaffold index + setup/conventions"
```

---

## Tasks 2–6: Per-Celina test-case files (FAN OUT IN PARALLEL)

Each of Tasks 2–6 is independent and produces one file. Dispatch one agent per task in a single batch. **Every agent receives the same shared contract below**, plus its Celina-specific scope from spec §6.

### Shared contract for every Celina-file agent

1. **Read first (sources of truth — never invent):**
   - The relevant `docs/bank-requirements/Celina N 2026.docx.md` (skip base64 image blobs).
   - The matching slices of `docs/api/REST_API_v3.md` and `docs/Specification.md` §17/§18/§20/§21 for the endpoints/entities/enums/rules in scope.
   - The relevant `api-gateway/internal/handler/*.go` for exact request fields, validation, and ownership checks.
   - The matching existing `test-app/workflows/*_test.go` files (to link, and to copy exact request/response shapes).
   - `docs/bank-requirements/Banka 2025 - E2E testovi.docx.md` and `Banka 2025 - odbrana flow.docx.md` for scenarios/provere in this Celina's domain.
2. **Produce** `docs/test-plan/celina-N-<name>.md` containing:
   - A short intro (scope + which spec sections it covers).
   - Test cases grouped by feature area, each following the spec §4 template EXACTLY (Feature, Spec ref, Existing test, Actor, Preconditions, Request with method+path+auth+JSON body, Verification, Expected with status+fields+side-effects, Negative siblings).
   - A per-file **field-validation matrix** table (field → valid example → each invalid form → expected error code) for every entity introduced in the Celina.
   - At the end, a **coverage rows** block: one line per feature/sub-feature/option `| feature | TC IDs | existing Go test | status(covered/partial/NO-ENDPOINT) |` — this feeds Task 7.
3. **Exhaustiveness (spec §5):** for every entity×field (valid + each invalid), every operation×actor (permitted/unpermitted/unauth/on-behalf + ownership violation), every enum value, every threshold (boundary each side), every documented success AND failure path. Each defense-flow provera in this domain → one named end-to-end scenario TC chaining the relevant cases.
4. **Accuracy:** paths/bodies/responses copied from the sources above. If a requirement has no matching endpoint, still write the TC and mark it `NO-ENDPOINT`. Use the standard error codes. Always assert side-effects for money/state changes, not just status.
5. **Return** (as the agent's final message) the file's coverage rows block so the orchestrator can assemble the matrix.

### Task 2: `celina-1-user-management.md`
- [ ] Scope = spec §6 "celina-1". Emphasis: login matrix (employee/client/wrong-pass/no-user/inactive), brute-force lockout boundary (4th ok, 5th locks; lock email; reset unlocks) — **explicitly note the 3-vs-5 attempt and 10-vs-30-min discrepancy across Celina1/E2E/Spec and test the implemented value**; password constraints (each violation); employee CRUD + activation + deactivation→session kill; RBAC per-permission gating; client field validation; token lifecycle (refresh/logout/revoke-all/sessions/login-history).
- [ ] Run the shared contract. Commit: `docs(test-plan): celina 1 user-management cases`.

### Task 3: `celina-2-core-banking.md`
- [ ] Scope = spec §6 "celina-2". Cover all account kinds × subtypes × auto-card; company types; payments (same/cross currency, fee stacking to bank, recipients, history); transfers (own accounts, reserved funds); menjačnica (2-leg, commission); cards (max-2 constraint, virtual usage types, brands, PIN lock, block/unblock, temp-block expiry, no-reactivate, authorized persons); loans (all types × interest types, approval limit gate, disbursement saga, installment formula, variable rate, auto-deduction success/fail).
- [ ] Run the shared contract. Commit: `docs(test-plan): celina 2 core-banking cases`.

### Task 4: `celina-3-securities.md`
- [ ] Scope = spec §6 "celina-3". Cover exchanges/hours toggle + closed/after-hours; listings + client visibility restriction; all order types × AON × margin × buy/sell with pricing/commission formulas; agent limit & approval boundary + supervisor approve/decline (once-only, illegal transitions) + settlement-passed decline-only + resets + order review; portfolio P/L + sell + make-public; dividends (formula, routing fallback, tax exemption); capital-gains tax (15%, monthly, loss=none, manual trigger, portal, yearly report, PDF, discrepancy flag); recurring orders; watchlist; price alerts.
- [ ] Run the shared contract. Commit: `docs(test-plan): celina 3 securities cases`.

### Task 5: `celina-4-otc-and-funds.md`
- [ ] Scope = spec §6 "celina-4". Cover OTC stocks; OTC options negotiation (offer/counter each-field/accept/withdraw, deviation color bands, valid/expired filter, no-action-after-settlement); agreement→contract+premium transfer+share lock; SAGA exercise 5 phases + each compensation path + retry≤3→admin alert + double-reserve prevention + funds-consumed-before-exec + CHECK_STATUS; multiple-contracts-per-seller share accounting; option/premium tax (seller-at-accept, buyer-exercise formula, expired, aktuar/bank exemption); investment funds full lifecycle (create, discovery+stats with min-snapshot gate, invest/redeem, supervisor-on-behalf no-fee vs client fee, partial liquidation+notify, block-deposit-while-withdrawal-pending, reject-below-min, NAV/position recompute, fund dividends, ownership transfer on permission removal, Moji fondovi views); bank-profit portal.
- [ ] Run the shared contract. Commit: `docs(test-plan): celina 4 otc-and-funds cases`.

### Task 6: `celina-5-cross-bank.md`
- [ ] Scope = spec §6 "celina-5". Cover two-stack bring-up (bank1/bank2, `gen-bank-stacks.py`, peer registration with base_url prefix, distinct OWN_BANK_CODE); inter-bank 2PC payment (success, cross-currency, **fee to receiving bank**, audit-trail fields, Not-Ready/inactive recipient, 10s timeout→cancel+refund, insufficient funds, any-step rollback); cross-bank OTC (discover peer offers, negotiate, accept, SAGA exercise both sides, compensation, CHECK_STATUS, participant-id legs, buyer holding, reservation cleanup); SI-TX **protocol conformance** asserting bodies match `contract/sitx/testdata/*.json` + protocol spec (signed-amount tagged-union postings, `{vote,reasons}`, `transactionId`, bare `/public-stock`, display-name `/user`, money-as-number); frozen-route assertions (e.g. `GET /negotiations/:rid/:id/accept`); adversarial ownership/double-charge cases.
- [ ] Run the shared contract. Commit: `docs(test-plan): celina 5 cross-bank cases`.

---

## Task 7: `cross-cutting-verification.md`

**Files:** Create `docs/test-plan/cross-cutting-verification.md`

- [ ] **Step 1: Read sources** — `verification-service` handler + `docs/Specification.md` verification section + `test-app/workflows/verification_test.go`, `mobile_auth_test.go`, and `helpers_test.go` verification helpers; enums `verification_method` (`code_pull`, `qr_scan`, `number_match`, `email`); config `VERIFICATION_CHALLENGE_EXPIRY=5m`, `VERIFICATION_MAX_ATTEMPTS=3`.
- [ ] **Step 2: Write cases** following the template: full challenge flow per method (request → receive code via Kafka/mobile inbox → submit → action proceeds); negatives: wrong code → rejected; expired challenge (after 5m) → rejected; max attempts (3) → transaction cancelled; `verification.skip` bypass for supervisor/admin. Include the exact endpoints to request/submit a challenge and how a gated action (payment/transfer/OTC exercise) references it.
- [ ] **Step 3: Commit** — `docs(test-plan): cross-cutting verification cases`.

---

## Task 8: Assemble `coverage-matrix.md`

**Files:** Create `docs/test-plan/coverage-matrix.md`

- [ ] **Step 1:** Collect the coverage-rows blocks returned by Tasks 2–7.
- [ ] **Step 2: Write the matrix** — one section per Celina; columns `| Feature / sub-feature / option | TC IDs | Existing Go test | Status |`. Status ∈ {covered, partial, NO-ENDPOINT}. Include a top summary: counts of features covered/partial/gap per Celina.
- [ ] **Step 3: Gap list** — a section listing every `partial` and `NO-ENDPOINT` row with one line on what's missing (no silent caps).
- [ ] **Step 4:** Update `README.md` coverage dashboard with the summary counts.
- [ ] **Step 5: Commit** — `docs(test-plan): coverage matrix + dashboard`.

---

## Task 9: Verification pass (prove the plan is accurate & complete)

**Files:** none (verification only); fixes go back into the relevant file.

- [ ] **Step 1: Link check** — every "Existing test" reference across all files resolves to a real `test-app/workflows` `Test...` function. Fix dangling refs.
  Run: `grep -rno 'test-app/workflows/[A-Za-z0-9_]*\.go::[A-Za-z0-9_]*' docs/test-plan/ | while IFS= read -r ...` (verify each path/function exists). Expected: no unresolved references.
- [ ] **Step 2: Spot-execution** — bring up the stack (`make docker-up` + seeder). For EACH Celina file, execute at least one POSITIVE and one NEGATIVE documented request with `curl` against `http://localhost:8080`, confirming the path, body shape, and expected HTTP status match reality. Record results inline in a short "spot-check log" at the bottom of `README.md`. Fix any path/body/status that didn't match.
  Expected: each spot-checked positive returns its documented success status; each negative returns its documented error code.
- [ ] **Step 3: Completeness pass** — re-read spec §6; confirm every listed feature/sub-feature/option has ≥1 positive and ≥1 negative TC OR an explicit gap row in the matrix. Add any missing TCs.
- [ ] **Step 4: Format check** — ensure no `<TBD>`/`TODO`/empty sections remain: `grep -rnE 'TBD|TODO|FILL[ _]IN|\.\.\.$' docs/test-plan/` returns nothing meaningful.
- [ ] **Step 5: Commit** — `docs(test-plan): verification pass — link check, spot-execution, completeness`.

---

## Self-Review (run before declaring done)

1. **Spec coverage:** every spec §6 area has a task (Tasks 2–7) and a matrix row (Task 8). ✓ mapping:
   - spec §6 celina-1 → Task 2; celina-2 → Task 3; celina-3 → Task 4; celina-4 → Task 5; celina-5 → Task 6; cross-cutting verification → Task 7; matrix → Task 8; verification → Task 9.
2. **Placeholder scan:** the `<...>` tokens in the template are intentional fill-ins for case authors, not plan placeholders; every task states concrete sources, outputs, and commit messages.
3. **Consistency:** ID scheme `TC-C<n>-<AREA>-<nnn>`, the §4 template, and the coverage-rows block format are identical across Tasks 2–8.
4. **VERSION:** bump a PATCH and sync `api-gateway/internal/version/version.go` in the final commit (docs change per CLAUDE.md). (Spec commit already bumped 2.16.12→2.16.13; bump again only if a later commit lands separately.)

## Out of scope
- New executable Go tests (we link existing ones, don't author new ones here).
- Fixing bugs the plan surfaces (log as matrix gaps; fix separately).
