# Service Decoupling Program — Design

**Date:** 2026-06-08
**Status:** Design (awaiting review)
**Author:** brainstorming session (lukasavic + Claude)

## 1. Problem

The backend is a Go microservice monorepo where services are **deeply coupled through
synchronous gRPC reads of rarely-changing reference data on the hot path**. A rich Kafka
event ecosystem already exists (≈90 topics), but **every service consumes only its own
events** — nobody maintains a local read-model from another service's events. The eventing
infrastructure is half-built and idle; the cross-service network hop is paid on every
loan approval, card op, account-event email, cross-bank display, and client-limit set.

This program decouples services by **denormalizing reference data into local read-models
fed by events**, **relocating data/routes that sit in the wrong service**, and **coarsening
the remaining chatty gRPC**.

### 1.1 Coupling map (evidence)

Cross-service synchronous gRPC edges fall into two categories with **opposite** treatment.

**Command edges — money/state mutation. Stay synchronous + saga. NOT denormalizable.**

- `transaction → account` (UpdateBalance, ReserveFunds, Release, PartialSettle)
- `transaction → exchange` (Convert — needs live rate)
- `transaction → verification` (GetChallengeStatus — gate)
- `credit → account` (Debit/Credit/UpdateBalance — disbursement & installments)
- `stock → account` (Reserve, CreditAccount, PartialSettle, Payout — settlement)
- `interbank → account / interbank → stock` (cross-bank saga postings)

**Reference-read edges — rarely-changing data fetched live. The coupling smell. Denormalizable.**

| Edge | RPC | Data | Change rate |
|---|---|---|---|
| `credit → user` | GetEmployeeLimits | MaxLoanApprovalAmount (approval gate) | rare |
| `credit → client` | GetClient | email/name (notification) | rare |
| `card → client` | GetClient | client profile/validation | rare |
| `client → user` | GetEmployeeLimits | employee limit (to cap client limit) | rare |
| `account → client` | GetClient | email/name (account-event email) | rare |
| `interbank → client/user` | GetClient/GetEmployee | display names | rare |
| `stock → client/user` | GetClient/GetEmployee | profile / actuary limits | rare |
| `transaction → account` | GetAccountByNumber | number→id/owner/currency/kind (stable) + balance/spending (volatile) | mixed |

### 1.2 Key findings

1. **Events are notification signals, not state-transfer events.** Payloads carry `{id, action}`,
   not the new state. `EmployeeLimitsUpdatedMessage` = `{EmployeeID, Action}` (no values);
   there is **no `ClientUpdatedMessage` struct** though `client.updated` is a topic. Building
   replicas requires **enriching events to carry full current state + a monotonic version**.
2. **The limits architecture is half-wired.** Two disconnected systems exist:
   - **Account spending limits** (`account.DailyLimit/MonthlyLimit` + `DailySpending`) are the
     **authoritative, enforced** limits (mutated atomically in `ledger_repository.UpdateBalance`;
     pre-checked in transaction-service).
   - **Client limits** (`client.DailyLimit/MonthlyLimit/TransferLimit`) are set and capped by
     employees but **never propagate to accounts and are never read at transaction time** —
     transaction-service has no client-service client. **Client limits are stored but unenforced.**
3. **One middle-man on client limits.** The gateway already calls client-service directly for
   `PUT /clients/:id/limits`. The *only* place user-service acts as a middle-man for client limits
   is `BlueprintService.applyClientBlueprint`, which does a synchronous `clientClient.SetClientLimits`.
4. **Caching is internal-only and appropriately scoped.** Each service caches its own entities
   (`client:id:`, `account:num:`, `employee:id:`, `rate:`, `security:stock:`); credit/transaction/
   interbank/verification/notification have none. These help the serving side, not the cross-service
   hop. No service needs *more* cache; replicas will reduce reliance on existing caches.

## 2. Principles

- **P1 — Denormalize reference reads.** Rarely-changing data another service reads on the hot path
  becomes a local **Postgres replica** fed by enriched events, with a **hybrid lazy gRPC fallback**
  on miss.
- **P2 — Fix ownership / kill middle-men.** Data and routes owned by the wrong service are relocated
  to the owner; no service is a pure pass-through. **Sole exception: interbank-service, which MUST be
  the middle-man for all SI-TX** (cross-bank protocol is frozen — do not refactor its verbs/paths).
- **P3 — Coarsen "sync" gRPC.** Where gRPC remains and a single flow makes several round-trips just to
  assemble or replicate state, collapse them into **one coarse batch/snapshot RPC**.

## 3. Shared pattern (every reference-read edge)

1. **Enrich the source event** to carry the full current entity state plus a **monotonic version**
   (reuse the owner entity's existing `Version int64`). Keep existing notification events intact —
   adding fields is backward-compatible (consumers ignore unknown fields).
2. **Consumer adds a Postgres replica table** `<entity>_replica` holding only the fields it uses +
   `version` + `updated_at`. Auto-migrated on startup like every other table.
3. **Consumer adds a Kafka consumer** that **idempotently upserts** the replica via
   `clause.OnConflict{}`, applying an event **only if `incoming.version >= stored.version`**
   (monotonic, tolerates out-of-order/duplicate delivery). Deletes/deactivations set a `status`/
   soft-delete column rather than removing the row.
4. **Service layer reads the replica** instead of gRPC.
5. **Hybrid lazy fallback:** on a replica miss, do **one** sync gRPC read, **backfill** the replica,
   and continue. Rollout never breaks; the replica self-heals; the gRPC client survives only as the
   fallback path, off the hot path. Steady-state consistency is eventual; on-miss is strong.

### 3.1 Coarse sync RPC (P3)

Each owner service exposes **one** coarse replication RPC used for backfill instead of many
fine-grained reads:

- `GetSnapshot(ids...)` / `ListChangesSince(version|timestamp)` returning full entity state.

Consumers use it for cold-start warmup and bulk backfill; the per-entity `GetX` fallback remains for
single-row misses. This is also where transaction-service's double `GetAccountByNumber` resolution
per transfer collapses into local replica reads (SP-3) — eliminating the chattiness entirely.

## 4. Sub-projects

| SP | Scope | Principle |
|---|---|---|
| **SP-0** | **Middle-man audit** — sweep all gateway routes + cross-service calls for pass-through owners beyond the known blueprint case. Produce a findings list; fold fixes into the relevant SP. | P2 |
| **SP-1** | **client-profile replica** (name, email, jmbg, status) consumed by credit, card, account, interbank, stock. Requires a `ClientUpsertMessage` (enrich `client.created`/`client.updated` to full state + version). | P1 |
| **SP-2** | **employee+limits replica** (name, role, permissions, MaxLoanApproval, MaxClient*Limit, actuary) consumed by credit (approval gate), stock (actuary), **client-service (the `MaxClientDailyLimit`/`MaxClientMonthlyLimit` cap when setting client limits — replaces the `client → user` read)**, and **auth-service (employee roles/permissions/name for JWT minting on every login/refresh — see SP-0 finding)**. Requires enriching `user.employee-updated` / `user.employee-limits-updated` / `user.actuary-limit-updated` to carry values + version. **Eventual + fallback** (money-adjacent staleness accepted; few-second window on a limit decrease, bounded by event lag, healed on miss). | P1 |
| **SP-3** | **account-metadata replica** (number→id, owner_id, currency, kind, status) consumed by transaction, interbank, stock, credit (resolution reads only). **Balance/spending EXCLUDED** — stays authoritative in account-service; enforcement never reads the replica. | P1 + P3 |
| **SP-4** | **client-limit ownership → client-service only.** Remove `applyClientBlueprint`'s `SetClientLimits` cross-call. Client-limit blueprints are applied by **client-service directly** (gateway → client-service), **never over user-service**, with **no events for the write**. user-service keeps employee/actuary blueprints. | P2 |
| **SP-5** | **client-limit → account-limit propagation.** `client.limits-updated` (enriched w/ values + version) → **account-service consumes → applies the policy as per-account DailyLimit/MonthlyLimit caps for all that client's accounts.** Makes the dead client-limit feature enforced, event-driven. Also collapses the `client → user` cap read into SP-2's replica (client-service reads the employee cap locally when setting client limits). | P1 + P2 |
| **SP-6** | **coarse sync RPCs** (P3) — add `GetSnapshot`/`ListChangesSince` to client/user/account services for replica backfill; verify transaction's resolution reads are fully local post-SP-3. | P3 |

## 5. Limits resolution (decision)

- **client-service owns the client-level limit *policy* (sole manager).** Gateway → client-service
  directly for both explicit sets and blueprint application. No user-service involvement (SP-4).
- **account-service owns per-account *enforcement* limits (authoritative, unchanged).**
- **`client.limits-updated` → account-service applies the policy to that client's accounts (SP-5).**
- Enforcement stays where the money moves. The only deliberate cross-service data copy is this
  one event-driven propagation, plus the read-only replicas. No two services *author* the same datum.

## 6. Cache plan

No new caches are warranted. Replicas absorb the cross-service reads, so existing owner-side caches
(`client:id:`, `account:num:`, `employee:id:`, `rate:`, `security:stock:`) become *lighter* and keep
current TTLs. credit/transaction/interbank stay correctly cache-free (replica-backed or
hot-path-authoritative). The replica tables are the local fast read; layering Redis in front of them
would be redundant. This will be re-confirmed per-SP, not assumed.

## 7. Testing strategy

Per CLAUDE.md testing requirement, every SP includes:

- **Consumer unit tests:** event → replica upsert; out-of-order/duplicate events respect version
  monotonicity; delete/deactivate sets soft-delete.
- **Service unit tests:** read hits replica; **miss falls back to sync gRPC and backfills**.
- **Integration tests (`test-app/workflows/`):** publish/trigger the owner change, assert the
  consumer's read reflects it (eventual), and assert business behavior (e.g., SP-5: changing a client
  limit changes what transaction-service enforces on that client's account).
- Use shared helpers from `contract/testutil/` and `test-app/workflows/helpers_test.go`.

## 8. Risks & mitigations

- **Stale money-adjacent limit (SP-2/SP-5):** few-second window on a limit *decrease*. Mitigated by
  rare changes, hybrid fallback (fresh on miss), bounded event lag, and account-service staying
  authoritative for spending enforcement.
- **Replica divergence / missed events:** version-guarded idempotent upserts + the coarse
  `ListChangesSince` backfill RPC allow periodic reconciliation; hybrid fallback masks transient gaps.
- **Event payload growth is breaking?** No — additive fields only; existing notification consumers
  ignore unknown fields (consistent with the API Versioning Compatibility Requirement spirit).
- **Async client-blueprint apply (SP-4):** application becomes direct gateway→client-service (still
  synchronous to the caller) — *not* fire-and-forget — so no validation-feedback loss.

## 9. Rollout order

`SP-1 (prove pattern) → SP-2 → SP-3 → SP-4 → SP-5 → SP-6`, with **SP-0 (audit)** run upfront and its
findings folded in. SP-1 is the first implementation target: widest blast radius, simplest data, no
money nuance — it proves the enrich-event + replica + fallback pattern end-to-end.

Each SP is its own spec → plan → implementation cycle. Per CLAUDE.md: bump `VERSION` per change, update
`Specification.md` (Kafka topics §19, message types, entities §18, gRPC §11), Swagger, and
`docs/api/REST_API_v3.md` for any route change (SP-4 touches a route).

## 10. SP-0 audit findings (completed 2026-06-08)

Swept every cross-service call and gateway route for misplaced ownership / pass-through middle-men.

**Write middle-men (cross-domain writes — the true ownership smell):**

- **Exactly one:** `user-service → client.SetClientLimits` via `BlueprintService.applyClientBlueprint`
  (client-type blueprints). → **SP-4.** Decision for the SP-4 plan: client-type limit blueprints
  should be **owned by client-service** (gateway → client-service directly to define *and* apply them),
  removing user-service from client limits entirely; user-service keeps employee/actuary blueprints.
  (Alternative — gateway reads the blueprint values from user-service then calls
  `client.SetClientLimits` directly — still removes user-service as the *executor* but leaves client
  templates in user-service; rejected as less aligned with "client-service is the sole manager.")
- **Not middle-men (legitimate cross-service commands, leave as-is):**
  `stock → account.CreateBankAccount` (fund provisioning — stock owns the fund, adds its own logic);
  `credit/transaction/stock → account.UpdateBalance/Reserve/...` (money-movement saga commands —
  account owns the ledger and exposes mutation RPCs).

**New reference-read edges found (fold into SP-2, the employee replica):**

- **`auth-service → user.GetEmployee`** — fetches employee roles/permissions/name to mint JWT claims,
  on **every login and refresh**. auth already consumes `user.employee-created`, so the consumer
  scaffolding exists. → **Add auth-service as a consumer of the SP-2 employee replica.** This is the
  hottest of the employee reads and the highest-value addition surfaced by the audit.
- **`verification-service → auth.CheckBiometricsEnabled`** — reads an auth-owned, rarely-changing
  biometrics-enabled flag during challenge creation. → **Low priority:** a small `account_flags`
  replica in verification fed by an enriched auth account event; defer unless cheap to include
  alongside SP-2. Not a middle-man.

**Confirmed exempt:** interbank-service's middle-man role for all SI-TX (protocol frozen).

**Net:** the audit confirms the program scope — one ownership fix (SP-4) plus the read-model
denormalization (SP-1/2/3), with SP-2 extended to cover auth-service's token-minting read.

## 11. Out of scope

- Command/money-movement edges (saga-based, stay synchronous).
- interbank-service middle-man role (frozen SI-TX protocol).
- Account balance/spending denormalization (authoritative, never replicated).
- New caching layers (none warranted).
