# Comprehensive Requirements-Driven Test Plan — Design

**Date:** 2026-06-07
**Status:** Approved (design)
**Author:** Claude (brainstormed with lukasavic)

## 1. Goal

Produce a complete, agent-consumable **test plan** that covers *every* feature, sub-feature, and option described in the five requirements documents (Celina 1–5), cross-referenced with the two test/defense documents. The plan must let any agent (or human) execute the tests immediately: each test case carries the concrete HTTP request (method, path, auth role, body), the expected outcome (status, response fields, side-effects), and its negative siblings.

Coverage requirement (verbatim from the request): **every feature, every feature inside a feature, every option specified — every negative and every positive outcome.** Protocol objects (cross-bank SI-TX wire messages) must match the protocol spec exactly; all other request/response objects only need equivalent *functionality*, not identical shapes.

## 2. Source material

Requirements (Serbian, in `docs/bank-requirements/`):
- **Celina 1** — Upravljanje korisnicima (user management: employees, clients, auth, login, brute-force lockout, RBAC, access/refresh tokens).
- **Celina 2** — Osnovno poslovanje banke (accounts, companies, payments, transfers, menjačnica/exchange, cards, loans/installments, TOTP verification).
- **Celina 3** — Trgovina na berzi (exchanges, listings stock/forex/futures/options, orders, agent limits & approval, portfolio, dividends, capital-gains tax, recurring orders, watchlist).
- **Celina 4** — Proširenje trgovine (OTC stocks & options, negotiation, option contracts, SAGA exercise, investment funds, option/premium tax, bank-profit portal).
- **Celina 5** — Komunikacija između banaka (inter-bank 2PC payments, cross-bank OTC SAGA, SI-TX protocol). Protocol: https://arsen.srht.site/si-tx-proto/.
- **TODO_final.pdf** — late refinement doc that *deepens* and *adds* requirements across all Celinas. Most items extend existing scopes (price alerts, watchlist, audit log, DCA, dividend payout, fund stats, OTC negotiation history) and are absorbed into the relevant Celina file. Two themes are genuinely cross-cutting and get their own file: (a) **notification coverage** — every significant event must emit email + in-app + (where applicable) mobile push; (b) **mobile-app features + Quick Approve**. These are first-class requirements.
- **Banka 2025 - E2E testovi** — frontend Gherkin scenarios (seed cases to fold in).
- **Banka 2025 - odbrana flow** — the defense "provere" (the exact happy-path flows graders run; these must each have an end-to-end test).

Current implementation (the test plan must map to what exists, not invent endpoints):
- `docs/Specification.md` §17 (API routes), §18 (entities), §20 (enums), §21 (business rules), §24–27 (funds, cross-bank, OTC).
- `docs/api/REST_API_v3.md` (~200 endpoints, all under `/api/v3`; v1/v2 retired → 404).
- `api-gateway/internal/handler/*` (exact request/response shapes + gateway validation + ownership checks).
- `test-app/workflows/*` (89 existing Go integration tests + `helpers_test.go`) — link, don't duplicate.
- `contract/sitx/testdata/*.json` + `docs/protocol/bank-to-bank-asset-exchange-protocol-spec.md` (the byte-exact protocol objects).
- Seeder, `docker-compose.yml`, and `bank1/`, `bank2/`, `scripts/gen-bank-stacks.py` (two-stack cross-bank bring-up).

## 3. Deliverable: directory layout

All files under `docs/test-plan/`:

```
docs/test-plan/
├── README.md                       # master index, how-to, links, coverage dashboard summary
├── 00-setup-and-conventions.md     # environment, seeding, base URL, seed creds, roles→tokens,
│                                   #   the test-case template, fast-path verification, common assertions,
│                                   #   how to read balances / scan Kafka, ID scheme
├── celina-1-user-management.md
├── celina-2-core-banking.md
├── celina-3-securities.md
├── celina-4-otc-and-funds.md
├── celina-5-cross-bank.md
├── cross-cutting-verification.md   # full TOTP/challenge mechanism: positive + negative
├── todo-final-notifications-and-mobile.md  # TODO_final.pdf: cross-cutting notification coverage + mobile-app features + Quick Approve
└── coverage-matrix.md              # every feature/sub-feature/option → TC IDs → existing Go test → status
```

Rationale: index + per-Celina files keeps each file readable and lets an agent pull just the slice it needs; the coverage matrix is the single checklist proving exhaustiveness.

## 4. Test-case template (uniform across all files)

```
#### TC-C2-PAY-001 · <title> (POSITIVE|NEGATIVE)
- **Feature:** <Serbian → English>  · **Spec:** Celina N §x.y  · **Existing test:** test-app/workflows/<file>.go::<Test> (or "—")
- **Actor:** <client | agent | supervisor | admin | employee-on-behalf | unauthenticated>
- **Preconditions:** <seeded/funded state, prior TC dependencies>
- **Request:** `<METHOD> <path>`
  - Auth: `Bearer <role token>`  (or none)
  - Body: `<JSON>`  (omit for GET/DELETE)
- **Verification:** fast-path (`verification.skip`) | full-flow (→ cross-cutting-verification.md) | n/a
- **Expected:** `<HTTP status>` · `<error.code or response fields>` · side-effects: `<balance deltas, status transitions, Kafka topics, ledger/audit entries>`
- **Negative siblings:** <inline list of the invalid variants and their expected error codes>
```

Conventions:
- **ID scheme:** `TC-C<celina>-<AREA>-<nnn>`, e.g. `TC-C3-ORD-014`. Variant suffixes `a/b/c` for actor variants of the same case. IDs are stable forever.
- **Error mapping** must use the project's standard codes (`validation_error`/400, `unauthorized`/401, `forbidden`/403, `not_found`/404, `conflict`/409, `business_rule_violation`/409, `rate_limited`/429, `internal_error`/500), per CLAUDE.md REST conventions.
- **Side-effects are mandatory** for any money-moving or state-changing case — never assert status code alone. Check balances (`available_balance` + ledger), Kafka events (`notification.send-email`, etc.), and entity status transitions, mirroring the existing workflow tests' rigor.

## 5. Coverage methodology (how exhaustiveness is guaranteed)

For each Celina file, enumerate cases along these axes and take the cross-product wherever meaningful:

1. **Entity × field validation** — for every field: one valid case + one case per invalid form (missing, wrong type, out of range, bad format, non-unique where unique required, future date where past required, etc.). Captured per-field in a validation matrix in each file.
2. **Operation × actor** — every create/read/update/delete/state-change run as: the permitted role (positive), each *unpermitted* role (403/404), unauthenticated (401), and employee-on-behalf (with and without the `*.on_behalf_client` permission). Ownership-violation variant for every caller-supplied resource id (per the Resource Ownership requirement).
3. **Every enum value** — a case per value, e.g. all 6 personal + 3 business account subtypes; all loan types (cash/housing/auto/refinancing/student) × interest types (fixed/variable); all order types (market/limit/stop/stop-limit) × modifiers (AON, margin) × direction (buy/sell); all security types; card brands; fee types.
4. **Every business rule / threshold** — a boundary case on each side: e.g. lockout at exactly 5 failed logins (4th allowed, 5th locks), fee thresholds (≥1000 RSD, ≥5000 RSD), agent daily-limit just-under vs just-over (auto-approve vs needs-approval), lot-size minimum, minimum fund deposit, tax formula boundaries.
5. **Every documented success path AND failure path** — including the explicit failure scenarios in the docs (insufficient funds, account inactive, market closed, settlement date passed, SAGA compensation, two-stack timeout/rollback, premium payment failure).
6. **The defense "provere"** — each numbered provera from `Banka 2025 - odbrana flow` becomes a named end-to-end scenario test that chains the relevant TCs (these are the must-pass grading flows).

The `coverage-matrix.md` lists every feature/sub-feature/option as a row; each row references the TC IDs that cover it and the existing Go test (if any). A feature with no covering TC is a visible gap.

## 6. Per-Celina coverage outline (what each file MUST contain)

This is the minimum scope checklist; the implementation will expand each into concrete TCs.

### celina-1-user-management.md
- Login: success (employee & client), wrong password, non-existent email, inactive account, `system_type` routing.
- Brute-force lockout: 5 consecutive failures → 10-min lock (note: E2E doc says "after 3" — flag the discrepancy and test the implemented value, currently 5/30-min per spec §21; document both), locked-account login blocked, email-on-lock, reset unlocks + resets counter.
- Password reset: request → email link → reset (link expiry — E2E says 15 min for reset, 24h for activation), reset unlocks account.
- Employee CRUD (admin only): create (all fields except password; active/inactive toggle), activation email + link expiry, set password (constraints: 8–32 chars, ≥2 digits, ≥1 upper, ≥1 lower — test each violation), edit (all fields except id/password), email uniqueness, JMBG 13-digit validation, deactivate → active sessions terminated + token rejected immediately (`token_expired`).
- Permissions/roles: assign roles, per-employee additional permissions, grant admin permission, RBAC enforcement (each permission gates its routes; unpermitted → 403; "users unaware of operations they can't perform").
- Client entity validation (email unique+format, phone digits/`+`, DOB not future).
- Access/refresh token lifecycle: refresh, logout (revoke), revoke-all, session list, login history.

### celina-2-core-banking.md
- **Accounts:** create tekući (RSD only) and devizni (EUR/CHF/USD/GBP/JPY/CAD/AUD); lični vs poslovni; all subtypes; with/without auto-card checkbox; initial balance; owner selection (existing vs new client created inline); bank-as-company; account number format (3-digit bank prefix + 18 digits); company creation (DOO/AD/Fondacija); list/filter/detail (personal vs business views); set status active/inactive; bank must keep ≥1 RSD + ≥1 FX account (delete guard).
- **Payments (`/api/me/payments`):** between different clients, same currency & cross-currency (FX conversion + commission); fee stacking (0.1% ≥1000, 5% ≥5000) credited to bank RSD account; recipient saved-list CRUD; payment history; verification fast-path + full-flow ref. Negatives: insufficient funds, inactive/nonexistent recipient, amount ≤ 0, over client daily/monthly/transfer limit, wrong-owner source account.
- **Transfers (`/api/me/transfers`):** between same client's own accounts, same & different currency; commission due to bank; reserved-funds semantics. Negatives mirror payments.
- **Menjačnica:** kursna lista, equivalence calculator, 2-leg via RSD with 0.5%/leg commission, rate source.
- **Cards:** request new card (max 2 physical per account constraint), auto-create on account, virtual cards (single_use/multi_use/unlimited + max_uses), brands (visa/mastercard/dinacard/amex), PIN (4 digits, bcrypt, lock after 3 fails), change limit, block (client) / unblock (employee), temporary block + auto-expiry, deactivate (and "deactivated card cannot be reactivated"), authorized persons for business accounts, multi-currency card fees.
- **Loans:** client submits request (all loan types, amount, term); employee approve/reject (max-approval-limit gate); disbursement saga (debit bank account, credit client, insufficient-bank-liquidity → 409); installment schedule + formula; fixed vs variable interest + bank margin + tier; automatic monthly installment deduction (success + failure notice); loan registry & detail views.

### celina-3-securities.md
- **Exchanges & hours:** list/detail; working-hours + holidays; admin toggle to open/close exchange for testing; order rejected when closed ("Berza je zatvorena"); after-hours (<4h to close) slow fill.
- **Listings:** stocks, forex pairs, futures (month codes / settlement), options chain; market-data reads, candles/history (1d/1w/1m/1y/5y/all); search/filter/sort; client visibility = stocks + futures only (forex/options hidden/forbidden).
- **Orders:** market (immediate, ask/bid pricing + commission min(14%,$7)), limit (favorable-price only, commission min(24%,$12), partial fill), stop (→ market on trigger), stop-limit (two-stage); AON (no partial fill, fail/pending); margin (permission/credit/cash prerequisites, initial margin cost = maintenance ×1.1, reject if neither met); buy & sell; account selection; on-behalf variant.
- **Agent limits & approval:** auto-approve when used+order ≤ limit; needs-approval when over limit, or `needApproval=true`, or limit exhausted; multi-currency limit check (conversion, no commission); supervisor approve/decline (once only; illegal transitions rejected); settlement-date-passed → decline-only; daily reset + supervisor manual reset; order review portal (supervisor); my-orders (agent/client); cancellation of unfilled portions; audit log entries.
- **Portfolio:** holdings, realized vs unrealized P/L, sell from portfolio (qty ≤ held), make-public (→ OTC), tax info section, dividend history.
- **Dividends:** quarterly payout, formula `qty × price × yield/4`, account routing fallback → RSD, 15% tax except bank-held.
- **Tax (capital gains):** 15% on realized profit (stock sale, futures settlement, option exercise, dividends); none on loss/unrealized; monthly calculation + auto-deduct (RSD conversion no-commission); supervisor manual trigger; tax-tracking portal; report by year; PDF export; profit-discrepancy flagging.
- **Recurring orders (DCA):** create/pause/resume/cancel; cron fires; insufficient-funds skip + notify.
- **Watchlist & price alerts:** CRUD, multiple lists, filter by type.

### celina-4-otc-and-funds.md
- **OTC stocks marketplace:** seller/buyer offers, accept/reject/cancel.
- **OTC options negotiation (intra-bank, client↔client):** make offer (qty, price/share, settlementDate, premium), counter-offer (each field mutable, history of old→new + who/when), accept, withdraw (deletes for both); color-coding deviation bands (±5/±20%); active-offers + concluded-contracts pages; filter valid/expired; can't counter/exercise after settlement date.
- **Agreement reached:** auto-create option contract, premium debited buyer → credited seller, seller's shares locked until exercise/expiry, contract appears in "Sklopljeni ugovori".
- **Exercise via SAGA:** the 5-phase flow (reserve funds → reserve shares → transfer funds → transfer ownership → final double-check) with per-phase compensation; in-the-money/profit display; exercise positive (buyer gets shares, seller paid, seller loses shares) + decline-when-unprofitable (lose only premium); SAGA failure paths: ownership-transfer failure → refund + return shares + mark "Poništena"; fund-refund retry ≤3 then alert admin; double-reserve prevention on concurrent negotiations; funds consumed before execution → cancel + refund; CHECK_STATUS resume.
- **Multiple contracts per seller:** sum of committed ≤ owned; expired-unused frees shares.
- **Option/premium tax:** seller premium 15% at accept; buyer exercise `15% × ((market−strike)×qty − premium)`; expired option → buyer premium loss reduces month's gains, seller no extra tax; aktuar/bank exemption (goes to bank profit).
- **Investment funds:** create (supervisor; unique name, min deposit, manager, auto RSD account); discovery page (filter/sort, stats: annual return, reward-to-variability, max drawdown, volatility, with min-snapshots gate); detailed fund view; client invest (≥ min deposit; from own account) / redeem (full or partial; to chosen account); supervisor deposit/redeem on behalf of bank (no conversion fee; client withdraw has fee); partial liquidation when illiquid + notify; block deposit while withdrawal pending; reject deposit < minimum; NAV / VrednostFonda / Profit / position percentage recalculation; dividend handling in funds (auto-inflow + reinvest/distribute); fund ownership transfer when supervisor permission removed; "Moji fondovi" tab (client & supervisor views).
- **Bank-profit portal (supervisor):** aktuar performances, bank positions in funds, deposit/withdraw to fund as bank.

### celina-5-cross-bank.md
- **Two-stack setup:** bring up bank1 + bank2 (`docker-compose` + `gen-bank-stacks.py`), register each as peer (`POST /api/v3/peer-banks`, base_url = peer's `/api/v3` prefix), distinct `OWN_BANK_CODE`.
- **Inter-bank payment (2PC):** success (Bank A → Bank B), bank identified by first 3 digits; Prepare→Ready (with end value, FX rate, commission)→Commit→credit; cross-currency; **fee due to receiving bank (Bank B)**; audit trail fields (sender bank, receiver bank, send time, receive time, status). Failures: Not-Ready (recipient inactive/nonexistent) → release reservation + notify; Bank B no-response 10s timeout → cancel + refund sender; sender insufficient funds → reject ("Nedovoljno sredstava"); any-step failure → full rollback.
- **Cross-bank OTC (supervisor↔supervisor and client↔client):** discover peer offers (`/public-stock`, `/negotiations`, `/user`), negotiate across banks, accept, SAGA exercise across both banks (RESERVE_FUNDS / RESERVE_SHARES_CONFIRM|FAIL / COMMIT_FUNDS / TRANSFER_OWNERSHIP / OWNERSHIP_CONFIRM / FINAL_CONFIRM), compensation + CHECK_STATUS retry; option legs carry participant ids; buyer receives holding; reservations cleaned both sides.
- **SI-TX protocol conformance (PROTOCOL OBJECTS MUST MATCH EXACTLY):** validate request/response bodies against `contract/sitx/testdata/*.json` and the protocol spec — signed-amount tagged-union postings, `{vote, reasons}`, `transactionId` correlation, bare `/public-stock`, display-name `/user`, money-as-number. Adversarial: ownership/auth on dispatch paths, double-charge prevention (per `project_crossbank_adversarial_findings`).
- **Frozen-route note:** cross-bank/peer-authenticated routes match the SI-TX spec verbatim even where they violate REST (e.g. `GET /negotiations/:rid/:id/accept`) — tests assert the spec'd verbs/paths, not "corrected" ones.

### cross-cutting-verification.md
- Real verification-challenge mechanism end-to-end: request challenge → receive code (Kafka/mobile inbox) → submit → action proceeds. Negatives: wrong code, expired challenge (5-min `VERIFICATION_CHALLENGE_EXPIRY`), max attempts (3) → transaction cancelled. All verification methods (`code_pull`, `qr_scan`, `number_match`, `email`). `verification.skip` permission path (supervisor/admin) bypasses.

### todo-final-notifications-and-mobile.md (TODO_final.pdf)
- **Notification coverage matrix** — every significant event must deliver email + in-app inbox + (where applicable) mobile push; assert the Kafka event (`notification.send-email`, `notification.mobile-push`) and the in-app inbox item for each:
  - Celina 1: account locked (after 5 failed logins).
  - Celina 2: payment executed, transfer executed, limit changed, card blocked/unblocked, credit created, credit approved.
  - Celina 3: order created→Pending, order approved, order rejected, order fully executed (isDone), order partially filled, order auto-cancelled (settlement expired); price-alert fired (threshold crossed / −5% intraday); dividend received; tax deducted; recurring-order skipped (insufficient funds).
  - Celina 4: OTC counter-offer received, OTC offer accepted, OTC offer withdrawn, option contract expiring in N days (e.g. 3 days before settlement).
  - For each: one positive (event fires → notification emitted) and the negative/edge (event does not fire when it shouldn't; in-app unread indicator).
- **Mobile-app features** (the "if time permits" set — test whatever the backend exposes; mark NO-ENDPOINT otherwise): view all own cards + block (note: unblock only via banker/in-branch, not mobile); view all accounts + balances; per-card transaction history (paginated); menjačnica access with all bank currencies; kursna lista + last-30-days history; upcoming loan-installment view.
- **Quick Approve** — approve a verification-required action directly from the push notification instead of typing a code; request expires if no response within 5 minutes; applies to ALL actions that require verification (cross-reference cross-cutting-verification.md). Positive (approve from push → action proceeds) + negatives (5-min expiry → request expires; approve after expiry → rejected).

## 7. Accuracy & non-duplication rules

- Paths, bodies, and response fields are copied from `REST_API_v3.md` / handlers / `Specification.md` — **never invented**. If a requirement describes a feature with no matching endpoint, the TC is still written and the matrix marks it `NO-ENDPOINT` (a real coverage gap to surface, not silently skip).
- Where an existing `test-app/workflows` test already covers a case, the matrix links it and the TC notes "existing"; we add TCs only for uncovered axes. No silent caps — if a feature is intentionally only partially covered, the matrix says so.
- Functional-equivalence rule: non-protocol request/response objects need only equivalent functionality; protocol objects are pinned to the wire spec.

## 8. Implementation approach

Fan out one agent per Celina file (celina-1…celina-5) plus one for cross-cutting-verification, each:
1. reads its Celina requirements section + the matching `REST_API_v3.md`/handler/`Specification.md` slices + related existing workflow tests,
2. emits the file's TCs following the template, exhaustively per §5,
3. returns its per-feature coverage rows.

Then reconcile all rows into `coverage-matrix.md`, write `00-setup-and-conventions.md` and `README.md`, and do a completeness pass (every feature/sub-feature/option from §6 has ≥1 positive and ≥1 negative TC, or a documented reason).

## 9. Testing of this deliverable

This deliverable is documentation, so "tests" = self-verification:
- **Spot-execution:** run a sample of the documented requests against a live local stack to confirm paths/bodies/expected statuses are accurate (at least one positive + one negative per Celina).
- **Link check:** every "existing test" reference resolves to a real `test-app/workflows` test.
- **Matrix completeness:** no feature row in `coverage-matrix.md` is left without TC IDs or an explicit gap label.
- No code/behavior changes to services are in scope; VERSION gets a PATCH bump for the docs addition per CLAUDE.md.

## 10. Out of scope

- Writing new executable Go tests (chosen artifact is the markdown plan; the matrix *links* existing Go tests but we don't generate new ones in this project).
- Fixing any bugs the plan surfaces (logged as gaps, fixed separately).
- Frontend/mobile UI testing beyond what the backend API exposes.
