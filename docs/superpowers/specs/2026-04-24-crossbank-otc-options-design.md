# Cross-Bank OTC Options — 5-Phase SAGA Design (Celina 5, Part 2)

## 1. Status

- **Status:** Draft
- **Date:** 2026-04-24
- **Spec number:** 4 of 4 (securities bank-safety stream)
- **Supersedes:** none
- **Depends on:**
  - **Spec 2** — Intra-bank OTC options (defines `OTCOffer`, `OptionContract`, negotiation state machine, local acceptance flow, `HoldingReservation` keyed on contract, exercise/expiry behaviour for same-bank counterparties). This spec extends those entities and reuses the negotiation state machine.
  - **Spec 3** — Inter-bank 2PC transport (defines the signed HMAC-authenticated peer channel, `Bank` registry, `InterBankTransfer` envelope with PREPARE/COMMIT/ABORT actions, `CHECK_STATUS` recovery, FX fallback). This spec adds new message `action` values that ride on that same transport, and reuses the Bank registry for peer routing.
- **Branch:** `feature/securities-bank-safety`
- **Scope:** Design-only. No implementation code in this document.
- **Target file in repo:** `docs/superpowers/specs/2026-04-24-crossbank-otc-options-design.md`.

---

## 2. Motivation

Celina 5 (Nova) describes the OTC options workflow end-to-end. The *intra-bank* half (two clients at the same bank negotiating and settling a call option on shares) is covered by Spec 2. The *cross-bank* half — the section titled **"Izvršavanje kupoprodaje"** (lines 161–258 of `docs/Celina 5(Nova).docx.md`) — is the subject of this spec.

The source document specifies a five-phase SAGA that moves money and share ownership between two banks atomically from the user's point of view, while being honest about the fact that cross-process atomicity does not exist. It enumerates concrete message shapes (`RESERVE_FUNDS`, `RESERVE_SHARES_CONFIRM`, `RESERVE_SHARES_FAIL`, `COMMIT_FUNDS`, `TRANSFER_OWNERSHIP`, `OWNERSHIP_CONFIRM`, `FINAL_CONFIRM`, `CHECK_STATUS`) and describes rollback paths for each phase.

The concrete business scenarios the user must be able to complete:

1. **Discovery.** A client or actuary at Bank A lists OTC offers and sees offers from clients/actuaries at Banks B, C, D alongside local offers, without any UI-level awareness of where each offer is served from.
2. **Counter-offer.** A client at Bank A can revise an offer that originated at Bank B; the revision is persisted on both sides and the seller at Bank B receives a notification.
3. **Acceptance → active contract.** Two clients at different banks agree; the system atomically debits the buyer's account for the premium, credits the seller's account, locks the seller's shares, and activates the `OptionContract` on both sides. Partial success is impossible; if anything fails, both sides return to the pre-acceptance state with no stuck funds and no stuck reservations.
4. **Exercise.** The buyer at Bank A exercises an active cross-bank contract; the strike × quantity moves from buyer to seller via the inter-bank 2PC channel and the shares move from seller to buyer via the ownership transfer phase. Again: atomic from the user's viewpoint.
5. **Expiry.** Settlement date passes without exercise; both banks transition the contract to `EXPIRED`, the seller's `HoldingReservation` is released, no funds move.
6. **Peer downtime.** Bank B is down while Bank A tries to accept or exercise. Spec 3's recovery mechanism kicks in; the saga is resumable via `CHECK_STATUS`; no money and no shares are lost.

Non-motivating (explicitly out of scope, see Section 14): custom option types (put options, American-style optionality on arbitrary dates), clearing house delegation, net settlement across multiple contracts.

---

## 3. Architecture Overview

### 3.1 Relationship to Spec 2 and Spec 3

This spec sits on top of two prerequisite designs. It does not re-define the entities they introduce; it *extends* them. The table below makes the dependency graph explicit.

| Concern | Owned by | This spec's relationship |
|---|---|---|
| `OTCOffer` entity, negotiation state machine, intra-bank acceptance | Spec 2 | Extends: adds `initiator_bank_code`, `counterparty_bank_code`, `public` (optional), `private` (optional) columns. State machine is unchanged. |
| `OptionContract` entity, intra-bank exercise, intra-bank expiry cron | Spec 2 | Extends: adds `buyer_bank_code`, `seller_bank_code` columns. A contract is "cross-bank" when the two codes differ. Exercise and expiry semantics for cross-bank contracts are defined here. |
| `HoldingReservation` keyed on `contract_id` | Spec 2 | Reused as-is. Only the *seller's* bank writes this row; buyer's bank has no holding reservation because shares do not yet exist there. |
| Inter-bank HMAC-signed transport, `Bank` registry, peer routing by bank code | Spec 3 | Reused as-is for message delivery. All cross-bank saga messages ride the same channel. |
| 2PC money transfer (PREPARE/COMMIT/ABORT with idempotency_key, FX fallback, `InterBankTransfer` durability row) | Spec 3 | Reused verbatim for **Phase 3 (transfer_funds)**. Premium/strike × qty is moved via the same two-phase mechanism. |
| `CHECK_STATUS` recovery over the inter-bank channel | Spec 3 | Reused verbatim. The saga executor in Phase N polls status via CHECK_STATUS just like a 2PC transfer does. |
| Intra-bank saga executor + `saga_logs` table in stock-service | Existing (Phase 2 of securities-bank-safety, already shipped) | Reused conceptually; new table `inter_bank_saga_logs` borrows the same shape but is durable across 5 phases rather than 3. |
| `ReserveFunds` / `PartialSettleReservation` / `CreditAccount` with `idempotency_key + memo` | Existing (Phase 2, shipped) | Reused verbatim for Phase 1 (buyer-side reserve) and Phase 3 commit leg. |

### 3.2 Prerequisite check (for the implementation plan that will follow this spec)

Before any code in this spec can be written, the following must be true:

- Spec 2 has shipped: `OTCOffer`, `OptionContract`, intra-bank acceptance flow, `HoldingReservation` keyed on contract.
- Spec 3 has shipped: `Bank` registry, HMAC transport, PREPARE/COMMIT/ABORT messages, `InterBankTransfer` durability rows, `CHECK_STATUS` recovery, FX fallback on buyer's side.

If either is missing, this spec's implementation plan must block on delivering the missing pieces first. Do not attempt to land cross-bank OTC on partial foundations.

### 3.3 High-level component diagram (text)

```
                    ┌─────────────────────────────┐
                    │   Bank A  (buyer's bank)    │
                    │                             │
  client ──HTTP──▶ api-gateway                   │
                    │     │                       │
                    │     ▼                       │
                    │  stock-service               │
                    │   ├── otc_service (mode:    │
                    │   │     crossbank_initiator)│
                    │   ├── inter_bank_saga_exec  │
                    │   ├── inter_bank_saga_logs  │
                    │   ├── otc_offers (+ bank_*)  │
                    │   └── option_contracts       │
                    │                             │
                    │  account-service (ReserveFunds,
                    │     CreditAccount, PartialSettle)
                    │                             │
                    └──────┬──────────────────────┘
                           │
                           │   Spec 3 inter-bank HMAC channel
                           │   (POST /internal/inter-bank/*, signed)
                           ▼
                    ┌─────────────────────────────┐
                    │   Bank B  (seller's bank)    │
                    │                             │
                    │  api-gateway (HMAC verify)  │
                    │  stock-service               │
                    │   ├── otc_service (mode:    │
                    │   │     crossbank_responder)│
                    │   ├── inter_bank_saga_exec  │
                    │   ├── inter_bank_saga_logs  │
                    │   ├── otc_offers (+ bank_*)  │
                    │   ├── option_contracts       │
                    │   ├── holding_reservations   │
                    │   └── holdings               │
                    │                             │
                    │  account-service (CreditAccount
                    │     for seller's premium)    │
                    └─────────────────────────────┘
```

### 3.4 Role asymmetry

Every cross-bank saga has a deterministic **initiator** and **responder** based on who clicked "Accept" (or "Iskoristi" for exercise). The initiator's bank drives the saga forward; the responder's bank only reacts to incoming messages and publishes status updates.

- Acceptance → initiator is **buyer's bank (A)**; responder is **seller's bank (B)**. Rationale: buyer clicked "Prihvati ponudu".
- Exercise → initiator is **buyer's bank (A)**; responder is **seller's bank (B)**. Rationale: buyer clicked "Iskoristi".
- Expiry → initiator is **either bank's cron** (whichever wakes first); the other becomes responder. Idempotency on `contract_id + EXPIRE` prevents double-processing.

The same `tx_id` is used across all 5 phases of one saga. Both banks persist one `inter_bank_saga_logs` row per (tx_id, phase, role).

---

## 4. Data Model — Extensions and New Entities

### 4.1 `OTCOffer` extensions

The entity is owned by Spec 2. This spec adds four columns:

| Column | Type | Nullable | Default | Semantics |
|---|---|---|---|---|
| `initiator_bank_code` | `varchar(3)` | yes | null | Bank code (see `Bank` registry, Spec 3) of the client/actuary who created the offer. `NULL` means the creator is local to whatever bank reads the row. Populated with the bank's own code for locally-created offers *after* we go cross-bank (migration back-fills). |
| `counterparty_bank_code` | `varchar(3)` | yes | null | Bank code of the counterparty who is negotiating or has accepted. `NULL` until a counterparty engages. |
| `public` | `bool` | no | `true` | When `false` the offer is not broadcast through the cross-bank discovery endpoint. (See Section 7; default is broadcast-everywhere.) |
| `private` | `bool` | no | `false` | When `true` the offer is visible only to a named `private_to_bank_code` peer. Present for future directed-offer workflows; this spec does not define UI for it but reserves the column. |
| `private_to_bank_code` | `varchar(3)` | yes | null | Set only when `private=true`. |

Indexes to add:

- `idx_otc_offers_crossbank_discovery` on `(public, private, status, updated_at)` to support the discovery query.
- `idx_otc_offers_counterparty_bank` on `(counterparty_bank_code)` to support "show me my own bank's offers engaged by peers".

**Migration note:** both back-fill rules apply at startup:

1. All existing (intra-bank) `OTCOffer` rows get `initiator_bank_code = self_bank_code`, `counterparty_bank_code = self_bank_code` when they have a counter-party, else `NULL`.
2. Both `public` and `private` default as described above for new rows; existing rows get `public=true, private=false` deterministically (so all shipped offers begin broadcasting unless explicitly hidden).

### 4.2 `OptionContract` extensions

| Column | Type | Nullable | Default | Semantics |
|---|---|---|---|---|
| `buyer_bank_code` | `varchar(3)` | no | self_bank_code | Bank where the buyer's account lives. |
| `seller_bank_code` | `varchar(3)` | no | self_bank_code | Bank where the seller's account and share holding live. |
| `crossbank_tx_id` | `uuid` | yes | null | The `tx_id` of the saga that produced this contract's ACTIVE status. Useful for recovery and audit linking. |
| `crossbank_exercise_tx_id` | `uuid` | yes | null | The `tx_id` of the exercise saga. Distinct from creation. |

A contract is `cross-bank` iff `buyer_bank_code != seller_bank_code`. This distinction is critical because cross-bank contracts cannot be exercised or expired via the intra-bank service methods — they must go through the saga executor.

Invariants checked at write time:

- If `buyer_bank_code = self`, then a buyer-side `Holding` row must be writable on this bank (for ownership transfer, phase 4).
- If `seller_bank_code = self`, then a `HoldingReservation` row must exist and be active for the contract.
- Both `buyer_bank_code` and `seller_bank_code` must reference a known row in the `banks` table (Spec 3).

### 4.3 `InterBankSagaLog` (NEW)

Separate table from Spec 2's intra-bank `saga_logs` and from Spec 3's `inter_bank_transfers`. The three serve different concerns:

- `saga_logs` — intra-bank multi-step DB writes inside one bank.
- `inter_bank_transfers` — single money movement across banks, 2PC.
- `inter_bank_saga_logs` (this spec) — multi-phase (up to 5) distributed saga where one phase is an `InterBankTransfer`.

**Schema sketch** (illustrative; not code):

| Column | Type | Nullable | Semantics |
|---|---|---|---|
| `id` | uuid (PK) | no | Row id. |
| `tx_id` | uuid | no | Saga id. Same across all 5 phases on both banks. |
| `phase` | enum | no | `reserve_buyer_funds`, `reserve_seller_shares`, `transfer_funds`, `transfer_ownership`, `finalize`. |
| `role` | enum | no | `initiator` or `responder`. |
| `remote_bank_code` | varchar(3) | no | The peer this row talks to. |
| `status` | enum | no | `pending`, `completed`, `failed`, `compensating`, `compensated`. |
| `offer_id` | uuid | yes | Filled during acceptance phases. |
| `contract_id` | uuid | yes | Filled once the contract exists (phase 5 onward on accept; from phase 1 for exercise). |
| `saga_kind` | enum | no | `accept` or `exercise` or `expire`. |
| `payload_json` | jsonb | no | Full message payload (before/after transforms). Redact nothing — this is audit. |
| `idempotency_key` | varchar(128) | no, **unique** | `{saga_kind}-{tx_id}-{phase}-{role}`; prevents duplicate execution on retry. |
| `error_reason` | text | yes | Last error string if `status in (failed, compensating)`. |
| `created_at` | timestamptz | no | Row creation. |
| `updated_at` | timestamptz | no | Last transition. |
| `version` | bigint | no | Optimistic lock field (see CLAUDE.md mandate). |

**Unique index:** `(tx_id, phase, role)` — one row per (saga, phase, role) on each bank. The `idempotency_key` unique index is redundant with this but explicit and easier to use from the repository layer.

**BeforeUpdate hook:** enforces `WHERE version = ?` and increments version, per the optimistic-lock mandate. Bulk transitions (e.g., overdue compensation sweep) use `SkipHooks`.

**Both banks write this row.** They are mirror states of the same saga; neither is authoritative. Reconciliation after downtime uses `CHECK_STATUS` to compare phase status across banks — if a bank's row says `completed` but the peer's says `pending`, the peer re-runs the phase using the idempotency key (which is a no-op).

### 4.4 `Bank` registry (Spec 3 — reused)

Unchanged. Must have an entry for every peer bank involved in OTC. The `public_key` and `shared_secret` columns are the HMAC material for inter-bank messages.

### 4.5 What we do **not** add

- No separate `CrossBankOffer` table. The `OTCOffer` row is canonical at the originating bank; the peer caches it, keyed by origin bank + offer id.
- No new holding-reservation variant. The seller-side `HoldingReservation` is identical to the intra-bank one — only the seller's bank writes it.
- No ledger table extension. Account-service ledger entries remain a per-bank concern; every credit/debit is already logged per Spec 3.

---

## 5. 5-Phase SAGA Flow

This section specifies, for the two primary kinds of cross-bank sagas (`accept` and `exercise`), the exact sequence of messages, state transitions, and durability writes.

### 5.1 Terminology

- **tx_id**: one UUID per saga instance. Generated by the initiator's bank when the saga starts. Propagated to the responder in every message.
- **idempotency_key**: derived from `{saga_kind}-{tx_id}-{phase}-{role}`. Written as the unique key on `inter_bank_saga_logs` and, for phase 1's `ReserveFunds`, on the `ReserveFunds` call into account-service (which Phase 2 of securities-bank-safety already supports as `idempotency_key`).
- **action**: the string identifier carried in message payloads (`RESERVE_FUNDS`, `RESERVE_SHARES`, `RESERVE_SHARES_CONFIRM`, `RESERVE_SHARES_FAIL`, `TRANSFER_FUNDS_PREPARE`, `TRANSFER_FUNDS_COMMIT`, `TRANSFER_FUNDS_ABORT`, `TRANSFER_OWNERSHIP`, `OWNERSHIP_CONFIRM`, `OWNERSHIP_FAIL`, `FINAL_CONFIRM`, `CHECK_STATUS`, `REVERSE_TRANSFER_PREPARE`, `REVERSE_TRANSFER_COMMIT`). Matches Celina 5's naming where applicable; adds new ones where Celina 5 did not name a message.

### 5.2 Saga kind: `accept`

Triggered when buyer clicks "Prihvati ponudu" on an offer whose counterparty bank code differs from the buyer's own bank. Inputs: `offer_id`, `buyer_account_id`, `buyer_client_id`.

#### Phase 1 — `reserve_buyer_funds`

**Bank A only (initiator).** No network hop yet.

1. Open DB transaction on stock-service's DB.
2. Insert `inter_bank_saga_logs` row: `(tx_id, phase=reserve_buyer_funds, role=initiator, saga_kind=accept, status=pending, offer_id, remote_bank_code=seller_bank_code, idempotency_key=<derived>)`.
3. gRPC call to account-service `ReserveFunds(account_id=buyer_account_id, amount=premium, memo="otc-crossbank-accept:{tx_id}", idempotency_key=<same as above>)`.
4. On success: update saga row `status=completed`. Commit DB tx.
5. On failure: update saga row `status=failed, error_reason=...`. Commit DB tx. Publish `otc.crossbank-saga-rolled-back`. Return error to caller (HTTP 402 or 409 depending on reason).
6. Only after DB commit: move to Phase 2.

**Message on the wire:** none yet. Phase 1 is local to Bank A.

**Idempotency:** the ReserveFunds gRPC call is keyed on the idempotency key, so a retry of phase 1 after a crash is a no-op on the account-service side.

#### Phase 2 — `reserve_seller_shares`

Bank A → Bank B.

1. Bank A writes initiator row `(tx_id, phase=reserve_seller_shares, role=initiator, status=pending, …)`. Commit.
2. Bank A sends HMAC-signed POST to Bank B at `/internal/inter-bank/otc/saga/reserve-seller-shares`. Body:
   ```json
   {
     "tx_id": "<uuid>",
     "action": "RESERVE_SHARES",
     "saga_kind": "accept",
     "offer_id": "<uuid>",
     "asset_listing_id": "<listing uuid>",
     "quantity": 50,
     "buyer_bank_code": "A",
     "seller_bank_code": "B"
   }
   ```
3. Bank B: on receipt,
   1. verify HMAC (Spec 3 middleware),
   2. upsert responder row `(tx_id, phase=reserve_seller_shares, role=responder, status=pending, …)` via `ON CONFLICT (tx_id, phase, role) DO NOTHING` — gives us idempotency for retries,
   3. in a DB transaction with `SELECT FOR UPDATE` on the seller's `Holding` row: check `available_quantity = quantity - reserved_quantity >= requested`; insert `HoldingReservation(contract_id=null for now, seller_holding_id, quantity=requested, expires_at=now()+offer_settlement_date, crossbank_tx_id=tx_id)`; increment `reserved_quantity`; save.
   4. update responder row `status=completed` and commit.
   5. respond to Bank A with `RESERVE_SHARES_CONFIRM`:
      ```json
      {"tx_id":"<uuid>","action":"RESERVE_SHARES_CONFIRM","shares_reserved":50,"reservation_id":"<uuid>"}
      ```
4. Bank A updates initiator row `status=completed` on receiving confirm. Commit.
5. If Bank B returns `RESERVE_SHARES_FAIL` (insufficient shares, listing not found, seller client deactivated), Bank A marks its row `status=failed` and jumps to compensation (§6). Responder row at Bank B stays `failed`.

**`contract_id` is not yet known at this phase on either bank** — acceptance has not produced a contract row yet. The seller-side HoldingReservation row is written with `contract_id=null` and `crossbank_tx_id=tx_id`. Phase 5 links the two by updating `HoldingReservation.contract_id` when the contract is materialised.

#### Phase 3 — `transfer_funds`

Bank A → Bank B, reusing Spec 3's **inter-bank 2PC transfer** machinery.

1. Bank A writes initiator row `(tx_id, phase=transfer_funds, role=initiator, status=pending)`.
2. Bank A calls the inter-bank transfer service (Spec 3) with:
   - `from_account=buyer_account_id`
   - `to_bank_code=seller_bank_code`
   - `to_account_alias=<seller's virtual settlement account>` — each bank publishes an OTC settlement account via Spec 3's bank registry. Premium lands there first, then Bank B internally credits the seller.
   - `amount=premium`
   - `currency=<offer.currency>`
   - `memo="otc-crossbank-accept:{tx_id}:premium"`
   - `parent_saga_tx_id=tx_id` (new optional field in `InterBankTransfer`; see Spec 3 addendum below.)
3. Spec 3's PREPARE/COMMIT runs:
   - PREPARE: Bank A debits reserved funds from the buyer (via `PartialSettleReservation` against the Phase 1 reservation — so the buyer's reservation is consumed, not released). Bank B earmarks on its settlement account.
   - COMMIT: Bank A finalises the debit; Bank B credits the seller's actual account (currency conversion if needed, using Bank B's inbound FX table from Spec 3).
4. On COMMIT success both banks mark their phase-3 rows `status=completed`.
5. On PREPARE failure at Bank B, Bank A's transfer row flips to `aborted`. Bank A marks phase 3 `status=failed` and runs compensation (§6 row "3").
6. On COMMIT failure (rare — one side committed, the other didn't reply): Spec 3's CHECK_STATUS reconciliation kicks in. Phase 3 stays `pending` until reconciliation resolves it.

**Why `PartialSettleReservation` instead of `CreditAccount + release`:** the Phase 1 reservation is the authoritative "this money is earmarked for this saga" marker. Consuming it with PartialSettleReservation atomically debits the reserved amount and shrinks the reservation — the ledger line references the same `idempotency_key` so nothing is double-spent on retry.

#### Phase 4 — `transfer_ownership`

Bank B → Bank A (Bank A requests; Bank B ships share ownership).

1. Bank A writes initiator row `(tx_id, phase=transfer_ownership, role=initiator, status=pending)`.
2. Bank A sends POST to Bank B at `/internal/inter-bank/otc/saga/transfer-ownership` with:
   ```json
   {
     "tx_id":"<uuid>",
     "action":"TRANSFER_OWNERSHIP",
     "asset_listing_id":"<uuid>",
     "quantity": 50,
     "from_client_id":"<seller_client_id>",
     "from_bank_code":"B",
     "to_client_id":"<buyer_client_id>",
     "to_bank_code":"A",
     "contract_id": null
   }
   ```
   (`contract_id` is still null; it gets materialised during Phase 5.)
3. Bank B:
   1. Upsert responder row.
   2. In a DB transaction with `SELECT FOR UPDATE` on seller's `Holding`: decrement `quantity` by 50 **and** `reserved_quantity` by 50 (the reservation is now consumed — it was protecting this decrement all along). Insert a `HoldingReservationSettlement` row (audit).
   3. Update responder row `status=completed`, commit.
   4. Respond `OWNERSHIP_CONFIRM`:
      ```json
      {"tx_id":"<uuid>","action":"OWNERSHIP_CONFIRM","assigned_at":"<ts>","serial":"<opaque seller-side reference>"}
      ```
4. Bank A on receiving confirm:
   1. In a DB transaction: upsert buyer's `Holding` row by `(client_id, listing_id)` — `ON CONFLICT` increment `quantity` by 50, else insert a new row with `quantity=50, reserved_quantity=0`.
   2. Insert an `OwnershipAuditEntry` (audit-only; shape borrowed from existing ownership lockdown work): `{client_id, listing_id, delta=+50, source="crossbank_otc_accept", tx_id}`.
   3. Update initiator row `status=completed`, commit.
5. If Bank B fails at its step (e.g., optimistic lock conflict): retry up to 3 times with backoff. After that, mark `status=failed` and run compensation (§6 row "4" — the painful one).
6. If Bank A fails at its step after receiving `OWNERSHIP_CONFIRM`: Bank B has already decremented; Bank A retries the upsert (idempotent via the audit row's unique constraint on `tx_id + delta`). If still failing after retry budget, the row stays `pending` for the reconciliation cron.

#### Phase 5 — `finalize`

1. Bank A writes initiator row `(tx_id, phase=finalize, role=initiator, status=pending)`.
2. Bank A creates the `OptionContract` row locally (or updates it if it already exists for this offer — should not) with:
   - `buyer_bank_code = self`
   - `seller_bank_code = remote`
   - `crossbank_tx_id = tx_id`
   - `status = ACTIVE`
   - all strike/premium/settlement fields copied from the offer at acceptance time (snapshot semantics).
3. Bank A sends POST to Bank B at `/internal/inter-bank/otc/saga/finalize` with:
   ```json
   {
     "tx_id":"<uuid>",
     "action":"FINAL_CONFIRM",
     "contract":{
       "id":"<uuid>",
       "buyer_client_id":"<uuid>",
       "seller_client_id":"<uuid>",
       "strike_price":"200.00",
       "quantity":50,
       "premium":"1150.00",
       "currency":"USD",
       "settlement_date":"2025-04-05",
       "buyer_bank_code":"A",
       "seller_bank_code":"B"
     }
   }
   ```
4. Bank B:
   1. Upsert responder row.
   2. Create its own mirror `OptionContract` row with `status=ACTIVE` and the same `id` as Bank A's. (Shared contract id — both sides use the UUID Bank A generated.)
   3. Update the `HoldingReservation` row from Phase 2 to reference this contract id: `UPDATE holding_reservations SET contract_id=? WHERE crossbank_tx_id=?`.
   4. Update responder row `status=completed`, commit.
   5. Respond with an echo `FINAL_CONFIRM_ACK` (plain 200 + empty body works too — this phase is mostly two-sided bookkeeping).
5. Bank A on ack: update its initiator row `status=completed`, commit. Publish `otc.crossbank-saga-committed` to Kafka with full contract snapshot.

#### Sequence diagram (text)

```
Bank A                             Bank B
  │                                  │
  │── Phase 1: ReserveFunds(local) ──┤
  │                                  │
  │── RESERVE_SHARES ──────────────▶ │ (phase 2 request)
  │                                  │  - validate holding
  │                                  │  - insert HoldingReservation
  │ ◀──────── RESERVE_SHARES_CONFIRM │
  │                                  │
  │── TRANSFER_FUNDS_PREPARE ──────▶ │ (Spec 3 2PC leg)
  │ ◀─────────── PREPARE_CONFIRM     │
  │── TRANSFER_FUNDS_COMMIT  ──────▶ │
  │ ◀──────────── COMMIT_CONFIRM     │
  │   (premium lands on seller's     │
  │    account; bank B credit_account)
  │                                  │
  │── TRANSFER_OWNERSHIP ──────────▶ │ (phase 4)
  │                                  │  - decrement holding
  │                                  │  - consume reservation
  │ ◀──────────── OWNERSHIP_CONFIRM  │
  │  (upsert buyer holding on A)     │
  │                                  │
  │── FINAL_CONFIRM ──────────────▶  │ (phase 5)
  │  create contract row locally     │  - create mirror contract row
  │                                  │  - link HoldingReservation→contract
  │ ◀─────────────── FINAL_CONFIRM_ACK
  │                                  │
  │  publish otc.crossbank-saga-committed
```

### 5.3 Saga kind: `exercise`

Same 5 phases, with four amount/semantics tweaks:

1. **Phase 1 amount:** `strike_price × quantity`, not `premium`. The premium was paid at acceptance time.
2. **Phase 2 logic:** the `HoldingReservation` for this contract **already exists** at Bank B from acceptance time. Phase 2 verifies the reservation is still active and of the correct quantity; it does **not** create a new one. If the reservation has been released (bug, or cron expired it early), return `RESERVE_SHARES_FAIL` with reason `reservation-missing`.
3. **Phase 3 amount:** `strike_price × quantity`, memo `otc-crossbank-exercise:{tx_id}:strike`.
4. **Phase 5 effect:** sets `OptionContract.status = EXERCISED` on both sides (not ACTIVE).

Triggered by buyer clicking "Iskoristi" on an active cross-bank contract. Input: `contract_id`.

All phase-by-phase mechanics mirror §5.2 exactly; only the amounts and the contract-status delta differ.

### 5.4 Saga kind: `expire`

Triggered by the expiry cron on either bank (§9). Fewer phases — only three actions, but we still record them in `inter_bank_saga_logs` for audit.

1. **Phase 1 (`expire_notify`, initiator only):** the bank whose cron woke first writes an initiator row and sends POST to peer:
   ```json
   {"tx_id":"<uuid>","action":"CONTRACT_EXPIRE","contract_id":"<uuid>"}
   ```
2. **Phase 2 (`expire_apply`, both sides):** each bank, on receiving or acting locally:
   - If `self == seller_bank_code`: release the `HoldingReservation` keyed on `contract_id` (decrement `reserved_quantity`; mark reservation `released_at=now()`).
   - If `self == buyer_bank_code`: no holding action (nothing was reserved buyer-side).
   - Both: update `OptionContract.status = EXPIRED`.
   - Both: write phase-2 `inter_bank_saga_logs` row `status=completed`.
3. **Phase 3 (`finalize`):** exchange `FINAL_CONFIRM`; publish `otc.contract-expired-crossbank`.

If the peer is unreachable, the initiator's row stays `pending`; the cron retries every 5 min.

### 5.5 Failure-point summary

| Where it can fail | What stays in place | What the saga executor does |
|---|---|---|
| Phase 1 (ReserveFunds) | nothing committed yet | return error to user, log `saga-rolled-back` |
| Phase 2 (RESERVE_SHARES) | funds reserved at Bank A | run compensation §6 row 2 |
| Phase 3 PREPARE | funds reserved at A; shares reserved at B | run compensation §6 row 3 (no money moved yet) |
| Phase 3 COMMIT one-sided | indeterminate until CHECK_STATUS | Spec 3 recovery cron resolves |
| Phase 4 at Bank B | money has moved A→B; B hasn't decremented shares | retry up to 3 times; on permanent fail run §6 row 4 (REVERSE_TRANSFER) |
| Phase 4 at Bank A (after B's confirm) | money moved, B decremented, A hasn't incremented | retry up to 3 times; on permanent fail log and let reconciliation cron pick up (A owes the buyer shares on paper; audit+ops) |
| Phase 5 | everything moved; only status flags not set | retry; final fallback is manual. Only a DB hard-fail would get us here. |

---

## 6. Compensation Matrix

This is the authoritative undo table. When a phase returns a failure, the initiator's saga executor runs the matching compensation row below; each compensation is itself idempotent (via `idempotency_key` per step).

| Failing phase | Bank A compensation (initiator) | Bank B compensation (responder) | Message exchange |
|---|---|---|---|
| **1 — reserve_buyer_funds** | none (ReserveFunds itself failed → nothing to undo). Saga row marked `failed`. | none (no message was sent). | none |
| **2 — reserve_seller_shares** | `ReleaseReservation(idempotency_key=phase1-key)` on buyer's account. Saga row (phase 1) transitions to `compensated`. | none (either B never reserved — FAIL path — or B reserved but A's message never reached B — CHECK_STATUS eventually releases it; see "orphan seller-side reservation" in §6.3). | optional `RESERVE_SHARES_ROLLBACK` to be explicit |
| **3 — transfer_funds** (PREPARE-level fail) | `ReleaseReservation` on buyer's account. | release seller-side `HoldingReservation` (decrement reserved_quantity, mark reservation `released_at`). | `RESERVE_SHARES_ROLLBACK` + Spec 3's `TRANSFER_FUNDS_ABORT` |
| **3 — transfer_funds** (post-COMMIT fail; rare, Spec 3 territory) | nothing — money already moved; Spec 3's CHECK_STATUS owns this. | ditto | Spec 3 `CHECK_STATUS` |
| **4 — transfer_ownership** | **REVERSE_TRANSFER saga** (see §6.2): Bank A claws back strike/premium from Bank B via an inter-bank 2PC transfer going the other direction, then Bank A releases/refunds the buyer. | release seller-side `HoldingReservation` (shares back to seller's available pool). | new action `REVERSE_TRANSFER_PREPARE` / `REVERSE_TRANSFER_COMMIT` on Spec 3 transport |
| **5 — finalize** | log `finalize-partial`; everything substantive has already committed. Retry budget: 5 attempts over 30 min; after that flag for manual reconciliation. | same | `CHECK_STATUS` for alignment |

### 6.1 Why compensation is ugly at Phase 4

By the end of Phase 3, the following facts are true in the world:

- Buyer's cash account (Bank A) has been debited for the premium (or strike×qty).
- Seller's cash account (Bank B) has been credited.
- Seller's shares at Bank B are still reserved but not yet decremented.

If Phase 4 fails, funds have already moved and we must claw them back to honour the "atomic-from-user-POV" promise. The responder's compensation (release share reservation) is trivial. The initiator's compensation is not — Bank A must run a *reverse* 2PC transfer.

Two options considered:

- **(a) Inter-bank reverse-transfer.** Bank A extends Spec 3's 2PC transport with `REVERSE_TRANSFER_*` actions. The reverse transfer is a regular 2PC with (from=seller's account @ Bank B, to=buyer's account @ Bank A, amount=same as forward, memo="crossbank-otc rollback {tx_id}"). If the seller has spent the credited funds between COMMIT and the rollback attempt, the reverse-transfer's PREPARE at Bank B fails with insufficient funds — at which point Bank B debits the seller to negative balance **iff policy permits** (Spec 3 FX/balance model). Most bank policies forbid going negative here; in that case the rollback becomes manual-reconciliation, and we publish `otc.crossbank-saga-stuck-rollback` to Kafka with full audit context.
- **(b) Leave as manual reconciliation always.** Unacceptable for a banking system.

**Decision:** (a), but accept that in the "seller spent the funds" edge case we escalate to ops. Spec 3 must be amended to define the `REVERSE_TRANSFER_*` actions. That amendment is described in §6.2 below and listed in Section 15 open questions (risk, not blocker).

### 6.2 `REVERSE_TRANSFER` addendum (Spec 3)

Spec 3 owns the inter-bank money transport. The cross-bank saga needs a way to reverse a previously-committed transfer when downstream saga phases fail. This is an addendum for Spec 3, listed here so the implementation plans can be synchronised:

- New actions: `REVERSE_TRANSFER_PREPARE`, `REVERSE_TRANSFER_COMMIT`, `REVERSE_TRANSFER_ABORT`.
- Request body includes `original_tx_id` (the forward-transfer's tx_id). The peer looks up its `inter_bank_transfers` row for `original_tx_id` and, if `status=committed` and no reverse exists, runs PREPARE on the opposite direction using the same amount, currency, memo template.
- Balance policy at responder: if seller's available balance < reverse amount, responder returns `REVERSE_TRANSFER_FAIL` with reason `insufficient-funds-at-responder`. Initiator then publishes the stuck-rollback Kafka event and flags manual reconciliation.
- Idempotent via `idempotency_key = reverse-{original_tx_id}`.

### 6.3 Orphan seller-side reservation

Edge case: Bank A sends `RESERVE_SHARES`, Bank B reserves successfully, Bank B's `RESERVE_SHARES_CONFIRM` reply never reaches Bank A (network partition). Bank A treats it as "phase 2 failed" and runs compensation. Bank B has an active `HoldingReservation` with no matching contract and no ongoing saga.

Resolution: Bank B's background cron scans `holding_reservations WHERE contract_id IS NULL AND crossbank_tx_id IS NOT NULL AND created_at < now() - interval '10 minutes'`. For each, it sends `CHECK_STATUS(tx_id)` to Bank A. If Bank A says `phase 2 failed` or `saga rolled back`, Bank B releases the reservation. If Bank A says `pending`, Bank B waits another 10 min.

---

## 7. Cross-Bank Offer Discovery + Caching

### 7.1 Design goal

A user opening the OTC portal should see local and remote offers in one merged list within a second, without hammering peer banks.

### 7.2 Endpoints

#### Internal (HMAC-authenticated, served by stock-service through api-gateway internal proxy)

- `POST /internal/inter-bank/otc/offers/list`
  - Request: `{"since": "<ISO ts>", "requester_role": "client" | "employee_actuary", "self_bank_code": "A"}`
  - Response: array of the peer's active OTC offers (`status in (open, counter)`), subject to visibility rules:
    - Role matching (Celina 5): clients see clients' offers; actuary employees see actuaries' offers.
    - `public=true AND private=false`, OR `private=true AND private_to_bank_code=requester.self_bank_code`.
- `POST /internal/inter-bank/otc/offers/{id}/fetch-one`
  - Bulk detail fetch when a UI needs the full negotiation history for a remote offer.
- `POST /internal/inter-bank/otc/offers/{id}/revise`
  - Peer-initiated counter-offer. Payload mirrors the intra-bank counter-offer body plus `requester_bank_code` and `requester_client_id_external` (opaque string; each bank hashes its own client ids for cross-bank exposure).
- `POST /internal/inter-bank/otc/offers/{id}/accept`
  - Peer-initiated accept. Kicks off the Phase 1..5 saga on the **peer** side as responder to Bank A's (initiator-side) saga. In practice: when Bank A's buyer clicks Accept, Bank A's stock-service starts the saga locally (Phase 1 is local) and then pings `/accept` on Bank B — which records the intent in its own saga log and moves its offer to `accepted_pending_finalize` status locally. Bank B does not need to explicitly "start" anything; it reacts to subsequent phase messages.

#### Public (served through api-gateway, authenticated via JWT)

- `GET /api/v1/otc/offers?include_remote=true&since={ts}`
  - `include_remote=false` → unchanged from Spec 2 (local offers only).
  - `include_remote=true` (default for the OTC portal UI):
    1. Query local `otc_offers` with the usual visibility filters.
    2. In parallel, for each active peer in the `banks` registry, hit `POST /internal/inter-bank/otc/offers/list` (with the caller's role and optional `since`).
    3. Merge results, sorted by `updated_at` desc.
    4. Apply a 60s TTL cache on peer responses, keyed by `(peer_bank_code, requester_role, since_bucket)` where `since_bucket = since rounded down to 60s`. Cache is in Redis via the existing stock-service cache wrapper.
    5. On cache hit: return cached peer data, re-query local only.
    6. Cache invalidation: on every **local** offer state change, publish a tiny Kafka message `otc.local-offer-changed`; peers consume it and bust their own "remote cache for bank X" key. This is best-effort — the 60s TTL is the floor guarantee.

### 7.3 Cache key & sizing

- Redis key pattern: `otc:remote:{self_bank_code}:from:{peer_bank_code}:role:{requester_role}:since:{bucket}`.
- Value: JSON array of offer-summary objects (id, asset, qty, price, status, updated_at, initiator_bank_code).
- TTL: 60 seconds.
- Size bound: each peer returns at most `MAX_OFFERS_PER_PEER` offers (default 500) — more than that requires pagination which is out of scope for v1.
- Degradation: if Redis is unavailable, fall through to direct peer queries every time. Latency rises but correctness holds.

### 7.4 Polling cadence

The Spec 2 UI already polls `/api/v1/otc/offers` on a 30s interval for local updates. We do **not** increase that cadence for cross-bank: the 30s local poll will naturally surface new remote offers on the next tick because `include_remote=true` is on by default, and the 60s remote-cache TTL is aligned with that cadence (every other local poll triggers a fresh peer fetch).

### 7.5 Rate-limiting at peers

Every `/internal/inter-bank/otc/offers/list` handler enforces a per-bank-code rate limit of 120 requests/minute. Exceeding it returns 429; initiator's cache treats 429 as "keep serving stale for up to 5 minutes" before escalating to a log-only alert.

---

## 8. REST API Surface

### 8.1 Public-facing routes (delta relative to Spec 2)

All public routes remain under `/api/v1/`. The only additions are extensions to existing Spec 2 endpoints:

| Route | Change | Notes |
|---|---|---|
| `GET /api/v1/otc/offers` | Added `include_remote=true|false` query param (default `true` for client role, `true` for actuary employees). Response items gain `initiator_bank_code` and `counterparty_bank_code` fields. | Back-compat: existing fields untouched. |
| `POST /api/v1/otc/offers/{id}/counter` | No URL change; body transparently handles remote offers. Gateway looks up `initiator_bank_code`; if remote, forwards via `/internal/inter-bank/otc/offers/{id}/revise`. | Returns the same response shape as the intra-bank path. |
| `POST /api/v1/otc/offers/{id}/accept` | Transparent routing based on offer's `initiator_bank_code`. When remote, this starts the 5-phase saga. Response: `{"saga_tx_id": "<uuid>", "status": "started"}` initially; a follow-up WebSocket push (via `notification.mobile-push`) delivers final `committed` / `rolled-back`. | HTTP response returns once Phase 1 completes (money reserved); full saga completion is async. |
| `POST /api/v1/otc/contracts/{id}/exercise` | Transparent routing based on `seller_bank_code`. When remote, starts exercise saga. Same async semantics. | |
| `GET /api/v1/otc/contracts/{id}/saga-status` | NEW. Returns the latest `inter_bank_saga_logs` phase/status for the contract (if cross-bank). For intra-bank contracts, returns the intra-bank saga_log state. | Used by the UI to show "Processing…" with phase progress. |

All public routes use `AnyAuthMiddleware` (client or employee token) + role/permission check `otc.trade` (reused from Spec 2).

### 8.2 Internal routes (new)

All under `/internal/inter-bank/otc/*`, all require HMAC auth (Spec 3 middleware).

| Route | Verb | Purpose |
|---|---|---|
| `/internal/inter-bank/otc/offers/list` | POST | peer discovery |
| `/internal/inter-bank/otc/offers/{id}/fetch-one` | POST | single offer detail |
| `/internal/inter-bank/otc/offers/{id}/revise` | POST | peer-sent counter-offer |
| `/internal/inter-bank/otc/offers/{id}/accept` | POST | peer-sent accept intent (side effect: create offer-side state mark `accepted_pending_finalize`) |
| `/internal/inter-bank/otc/saga/reserve-seller-shares` | POST | phase 2 |
| `/internal/inter-bank/otc/saga/transfer-funds` | POST | phase 3 (wraps Spec 3's PREPARE/COMMIT/ABORT endpoints internally — or the caller can go directly to Spec 3 transport) |
| `/internal/inter-bank/otc/saga/transfer-ownership` | POST | phase 4 |
| `/internal/inter-bank/otc/saga/finalize` | POST | phase 5 |
| `/internal/inter-bank/otc/saga/check-status` | POST | recovery; payload `{tx_id, phase?}` → returns `{phase, role, status, error_reason?}` |
| `/internal/inter-bank/otc/contracts/{id}/expire` | POST | peer-sent expiry notice |
| `/internal/inter-bank/otc/saga/reverse-transfer/prepare` | POST | compensation (Spec 3 addendum §6.2) |
| `/internal/inter-bank/otc/saga/reverse-transfer/commit` | POST | compensation |
| `/internal/inter-bank/otc/saga/reverse-transfer/abort` | POST | compensation |

### 8.3 Response and error conventions

All public routes follow CLAUDE.md's `apiError()` conventions and map gRPC codes per the service/handler layer. All internal routes return JSON with:

```json
{"ok": true, "tx_id": "…", "phase": "…", "status": "…"}
```

or

```json
{"ok": false, "error": {"code": "…", "message": "…", "retryable": true|false}}
```

`retryable=false` means the initiator should jump straight to compensation instead of retrying.

### 8.4 Swagger + REST_API_v1.md updates

Per CLAUDE.md's hard requirement:

- Every new public route above gains a Swagger godoc block and appears in `docs/api/REST_API_v1.md` under a new **"OTC Cross-bank"** section.
- Internal routes are **not** documented in REST_API_v1.md (they are bank-to-bank infra, not user-facing). They are documented in `docs/api/INTERNAL_INTER_BANK.md` alongside Spec 3's routes.

---

## 9. Expiry Cron — Cross-Bank Extension

### 9.1 Cron scope

The existing intra-bank OTC expiry cron (owned by Spec 2) runs every 5 minutes and processes:

```
SELECT * FROM option_contracts
 WHERE status = 'ACTIVE'
   AND settlement_date < now()
   AND buyer_bank_code = self_bank_code
   AND seller_bank_code = self_bank_code;
```

This spec adds a **second cron** (same schedule, different query) for cross-bank contracts:

```
SELECT * FROM option_contracts
 WHERE status = 'ACTIVE'
   AND settlement_date < now()
   AND (buyer_bank_code != self_bank_code OR seller_bank_code != self_bank_code);
```

Running both queries on the same bank means cross-bank rows will be picked up by one of the two participating banks' crons. Whichever wakes first wins the initiator role; the other reacts.

### 9.2 Per-contract algorithm

For each row returned by the cross-bank query:

1. Acquire a row-level lock: `SELECT … FOR UPDATE SKIP LOCKED`. Contracts already being processed by another worker are skipped.
2. Check `inter_bank_saga_logs` for an in-flight expire saga on this contract (`saga_kind='expire' AND contract_id=? AND status NOT IN ('completed','compensated')`). If present, skip — another worker or the peer is already on it.
3. Generate `tx_id`, write initiator phase-1 row, send `CONTRACT_EXPIRE` to the peer bank's `/internal/inter-bank/otc/contracts/{id}/expire`.
4. Apply local phase-2 effect (release reservation if self is seller; no-op if self is buyer; transition status to `EXPIRED`).
5. On peer ack: write phase-3 `finalize` rows on both sides, publish `otc.contract-expired-crossbank`.
6. On peer timeout: cron re-picks the row next cycle (still ACTIVE, settlement_date still past; the in-flight-saga check prevents double processing on retry because the phase-1 row exists).

### 9.3 Concurrency considerations

- Two banks' crons waking simultaneously on the same contract: whichever's phase-1 row commits first is the initiator; the other's attempt to insert the `(tx_id-2, phase=1, role=initiator)` row succeeds on its own side, then its `CONTRACT_EXPIRE` message is handled by the peer as a duplicate (peer has already transitioned to EXPIRED) — peer returns `already-expired`, the loser rolls back its own saga (idempotent, nothing to undo).
- **Lock strategy**: the `SKIP LOCKED` row lock on `option_contracts` prevents one bank from processing the same contract twice in the same sweep; cross-bank duplication is detected via the peer's response.

### 9.4 Escalation

If peer is unreachable for > 1 hour: publish `otc.contract-expiry-stuck` to Kafka, page ops, keep retrying. Contract remains `ACTIVE` from our side's POV until expiry fully succeeds — seller's reservation does **not** auto-release without peer confirmation, to avoid inconsistency.

---

## 10. Kafka Topics

### 10.1 New topics

| Topic | Payload shape (illustrative) | Producer | Consumer(s) |
|---|---|---|---|
| `otc.crossbank-saga-started` | `{tx_id, saga_kind, contract_id?, offer_id?, initiator_bank_code, responder_bank_code, started_at}` | stock-service (initiator) | notification-service (push to initiator's UI), analytics sink |
| `otc.crossbank-saga-committed` | `{tx_id, saga_kind, contract_id, committed_at, final_phase_durations}` | stock-service (initiator) | notification-service, analytics |
| `otc.crossbank-saga-rolled-back` | `{tx_id, saga_kind, failing_phase, reason, compensated_phases[], rolled_back_at}` | stock-service (initiator) | notification-service (error push), ops alerting (if `compensated_phases.length > 2` — i.e., we rolled back from deep) |
| `otc.contract-exercised-crossbank` | `{contract_id, tx_id, buyer_client_id, seller_client_id, strike_price, quantity, currency, exercised_at}` | stock-service (initiator) | notification-service, analytics |
| `otc.contract-expired-crossbank` | `{contract_id, expired_at, seller_bank_code, buyer_bank_code}` | stock-service (initiator of expire saga) | notification-service, analytics |
| `otc.local-offer-changed` | `{offer_id, bank_code, new_status, updated_at}` | stock-service | any peer's stock-service that has cached this offer; used for cache invalidation (§7.2) |
| `otc.crossbank-saga-stuck-rollback` | `{tx_id, contract_id, reason, audit_link}` | stock-service (on compensation deadlock) | ops pager |
| `otc.contract-expiry-stuck` | `{contract_id, peer_bank_code, hours_stuck}` | stock-service cron | ops pager |

### 10.2 Topic pre-creation

Per CLAUDE.md's hard requirement, stock-service must call `EnsureTopics` at startup for every new topic. Notification-service must do the same for the topics it consumes.

### 10.3 Message payload location

Typed structs live in `contract/kafka/messages.go` (new exported types `CrossBankSagaStartedMessage`, `CrossBankSagaCommittedMessage`, `CrossBankSagaRolledBackMessage`, `ContractExercisedCrossBankMessage`, `ContractExpiredCrossBankMessage`, `LocalOfferChangedMessage`).

### 10.4 Ordering and retry

- `otc.crossbank-saga-*` messages are produced only after DB commit (per CLAUDE.md).
- Producer uses the shared kafka-go producer with `RequireAll` acks.
- Consumers (notification-service) are idempotent on `tx_id` — repeated delivery of a `committed` event results in at most one push per contract_id.

---

## 11. Permissions

### 11.1 Reuse of `otc.trade`

The existing `otc.trade` permission (defined in Spec 2 for intra-bank OTC) covers both intra-bank and cross-bank acceptance/exercise from the user's point of view. No new permission is introduced for the public-facing routes.

### 11.2 New internal-only permission

Spec 3 introduces `inter_bank.peer`, an internal-service permission for HMAC-authenticated peer calls. Cross-bank OTC routes live under `/internal/inter-bank/otc/*` and are gated by `inter_bank.peer` the same way Spec 3's transfer routes are. No employee or client principal ever has this permission — it is carried only by the HMAC signing identity.

### 11.3 Role seeding

No changes to `EmployeeBasic`, `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin` seed permissions. `otc.trade` on `EmployeeAgent` (seeded in Spec 2) continues to suffice for employee actuaries initiating cross-bank negotiations.

### 11.4 Cross-bank visibility for actuaries

Internal `/offers/list` returns only offers whose originator role matches the requester role (Celina 5 requirement). No extra permission required; the service-layer filter enforces it.

---

## 12. Testing Strategy

### 12.1 Mock peer bank test harness

Cross-bank tests require a *second bank*. We introduce a lightweight **mock peer harness** (lives under `test-app/fixtures/mock-peer-bank/`), wired into the existing integration test runner.

The mock peer:

- Runs an HTTPS server on a random free port per test, configured with a fixed HMAC shared secret that the test registers into the main bank's `banks` table at setup time.
- Implements all `/internal/inter-bank/otc/*` and relevant Spec 3 transfer endpoints.
- Is driven by a scripted scenario object: `{"reserve_seller_shares": "success" | "fail:insufficient" | "timeout", "transfer_funds_prepare": "success" | "fail:down", "transfer_ownership": "success" | "fail:lock" | "delay:30s", ...}`. Tests configure per-phase behaviour to simulate failures at each stage.
- Exposes an in-memory holding and reservation store, so we can assert on peer-side state after each saga.
- Emits the same Kafka messages the real peer would (via the test Kafka) so that notification-service delivery paths are exercised.

Harness helpers live in `test-app/workflows/helpers_crossbank_otc_test.go`:

- `setupMockPeer(t, scenario)` returns a handle with url, stop func, and an `AssertHoldingReservation(contract_id, qty)` method.
- `registerPeerBank(t, peerHandle)` inserts into `banks` and hot-reloads the registry cache.
- `runCrossBankAccept(t, offerId)` / `runCrossBankExercise(t, contractId)` drive the full public-API flow and block until Kafka delivers `otc.crossbank-saga-committed` or `-rolled-back`.

### 12.2 Integration test cases (minimum 10)

1. **Happy path accept.** Buyer at bank A accepts seller-at-bank-B offer; verify (a) buyer's balance debited by premium, (b) seller's balance credited, (c) seller-side `HoldingReservation` created, (d) `OptionContract` rows exist on both sides with `status=ACTIVE`, (e) `otc.crossbank-saga-committed` Kafka event delivered, (f) buyer UI gets push notification.
2. **Happy path exercise.** Following test 1, buyer exercises; assert (a) buyer debited strike×qty, (b) seller credited, (c) seller's holding decremented, (d) buyer's holding incremented, (e) contract status `EXERCISED`, (f) `otc.contract-exercised-crossbank` delivered.
3. **Accept — phase 2 fail (insufficient shares).** Mock peer returns `RESERVE_SHARES_FAIL`; assert (a) buyer's funds released via `ReleaseReservation`, (b) saga logs show phase 1 `compensated`, phase 2 `failed`, (c) no `OptionContract` row on either side, (d) `otc.crossbank-saga-rolled-back` delivered.
4. **Accept — phase 3 PREPARE fail.** Peer's inter-bank transfer PREPARE fails; assert (a) buyer funds released, (b) peer's `HoldingReservation` released, (c) saga rolled back with `failing_phase=transfer_funds`.
5. **Accept — phase 4 fail at peer (after funds moved).** Peer fails to decrement holding; assert (a) REVERSE_TRANSFER saga runs and succeeds, clawing funds back from seller to buyer, (b) peer's reservation released, (c) `otc.crossbank-saga-rolled-back` delivered with `compensated_phases` including `transfer_funds`.
6. **Accept — phase 4 fail at peer, seller has spent the funds.** Mock peer reports seller account insufficient for reverse transfer; assert (a) saga transitions to `compensating` and stays there, (b) `otc.crossbank-saga-stuck-rollback` emitted, (c) ops alert surface.
7. **Network partition between phase 2 confirm and Bank A receipt.** Mock peer delivers CONFIRM then drops connection; Bank A's saga appears to fail phase 2. Assert Bank B's reconciliation cron detects orphan reservation after 10 min (fast-forward test clock) and releases it. Assert Bank A has rolled back cleanly.
8. **CHECK_STATUS recovery.** Mid-saga, Bank A is restarted. On startup, saga executor resumes the pending saga via `CHECK_STATUS`. Assert saga completes successfully and final state is consistent.
9. **Expiry — happy path.** Create a cross-bank active contract, fast-forward past settlement_date, trigger cron. Assert both banks flip to `EXPIRED`, seller's reservation released, `otc.contract-expired-crossbank` delivered.
10. **Expiry — peer down.** Peer unreachable at expiry time; assert contract stays `ACTIVE` from local side, saga log shows phase-1 pending, cron retries next cycle; after > 1 hour, `otc.contract-expiry-stuck` emitted.
11. **Discovery — merged list.** Create 3 local offers, mock peer returns 5 offers, call `GET /api/v1/otc/offers?include_remote=true`; assert 8 offers in response, sorted by `updated_at desc`, each with correct `initiator_bank_code`.
12. **Discovery — cache hit.** Two consecutive calls in < 60s; assert only one POST to `/internal/inter-bank/otc/offers/list` via mock peer's request log.
13. **Discovery — cache invalidation.** Mock peer publishes `otc.local-offer-changed`; assert main bank's Redis cache for that peer is busted; next call fetches fresh.
14. **Counter-offer cross-bank.** Bank A client counter-offers on Bank B's offer; assert (a) peer receives `/revise` call, (b) both sides' `OTCOffer` rows reflect the counter, (c) seller at Bank B receives notification.
15. **Accept when peer rejects HMAC.** Misconfigure HMAC secret on peer; assert call fails with 401, saga aborts, buyer funds released.
16. **Duplicate-accept idempotency.** Two rapid-fire accept clicks on the same offer; assert only one saga runs (second returns `already-in-progress` or joins the existing saga), one contract created, buyer charged once.
17. **FX — buyer account RSD, offer currency USD.** Phase 1 reserves RSD equivalent at buyer's bank's USD→RSD rate; Phase 3 transfer uses Spec 3's FX fallback to deliver USD to seller; assert balance accounting correct on both sides.
18. **Accept with `private=true` offer.** Private offer visible to Bank A is accepted by Bank A client; assert works. Same offer invisible to Bank C; assert Bank C's discovery does not return it.

### 12.3 Unit test coverage

- `inter_bank_saga_log_repository`: version conflict handling, ON CONFLICT behaviour, per-phase CRUD.
- `crossbank_saga_executor`: phase-by-phase unit tests with fake peer client and fake account-service client; every phase's success / failure / retry-then-succeed path.
- `otc_discovery_service`: merge logic, cache hit/miss, stale-while-error behaviour, rate-limited peers.
- Handler layer: HMAC verify rejection, payload validation, error→HTTP mapping.

### 12.4 Shared helpers

Per CLAUDE.md mandate, all shared verification/setup uses `contract/testutil/` (unit) and `test-app/workflows/helpers_test.go` (integration). No Kafka scanning, no HMAC signing, no saga driving may be inlined.

### 12.5 Pass criteria

- All integration tests pass under `make test` against Docker-Compose-spun infrastructure with the mock peer harness.
- Unit tests target coverage > 80% on new code in `inter_bank_saga_log_repository`, `crossbank_saga_executor`, `otc_discovery_service`, `otc_handler` (cross-bank paths).
- `make lint` clean on all touched services (stock-service + api-gateway).

---

## 13. Spec / REST Doc Updates

After implementation:

### 13.1 `docs/Specification.md`

- **Section 11 (gRPC services):** no changes (all cross-bank is via HTTP/HMAC).
- **Section 17 (API routes):** add the new `GET /api/v1/otc/offers?include_remote=` behaviour, the `POST /api/v1/otc/contracts/{id}/saga-status`, and notes on transparent routing for accept/exercise/counter. Mark the internal `/internal/inter-bank/otc/*` cluster in Section 17.2.
- **Section 18 (entities):** extend `OTCOffer` and `OptionContract` definitions; add `InterBankSagaLog`.
- **Section 19 (Kafka topics):** add the eight new topics.
- **Section 20 (enum values):** new enums for `inter_bank_saga_phase`, `inter_bank_saga_role`, `inter_bank_saga_status`, `saga_kind`. New `OptionContract.status=EXPIRED` value (if not already present from Spec 2).
- **Section 21 (business rules):** record the "cross-bank contracts route via saga, never intra-bank methods" invariant; the "seller reservation survives across accept→active→exercise" invariant; the "phase 4 rollback requires reverse-transfer" operational rule.
- **Section 6 (permissions):** reaffirm `otc.trade` covers cross-bank; note `inter_bank.peer` for internal routes.

### 13.2 `docs/api/REST_API_v1.md`

- New section **"OTC Cross-bank"** under the OTC chapter, covering:
  - Altered `GET /otc/offers` behaviour with `include_remote`.
  - Transparent routing of `counter`/`accept`/`exercise`.
  - New `GET /otc/contracts/{id}/saga-status` with response shape.

### 13.3 `docs/api/INTERNAL_INTER_BANK.md`

- Expand the Spec 3 internal doc to cover the new `/internal/inter-bank/otc/*` cluster, message shapes, HMAC requirements, and retry guidance.

### 13.4 Swagger

- Regenerate via `make swagger`; commit `api-gateway/docs/` alongside handler changes.

---

## 14. Out of Scope

The following are explicitly **not** in scope for this spec; they are either separate workstreams or explicitly deferred:

1. **Put options / American optionality / arbitrary-style options.** Celina 5 scope is European call options only (per the source document's examples). Extending to puts requires a different holding-reservation model (buyer reserves money + seller's shares work differently); punt.
2. **Multi-party (N > 2) sagas.** All sagas here are bilateral (initiator + responder). Three-way deals (broker in the middle) not supported.
3. **Clearing-house delegation.** Some real banks offload OTC settlement to a central clearing house; we settle directly.
4. **Net settlement across multiple contracts.** Each contract is settled on its own saga; we do not batch multiple contracts between the same two banks into one net transfer.
5. **Partial exercise.** If a contract is for 100 shares, the buyer can exercise all 100 or none. No partial (50 shares today, 50 later).
6. **Contract assignment / resale.** Once an `OptionContract` is ACTIVE, the buyer cannot resell or transfer it to another client. Life-cycle is (ACTIVE → EXERCISED) or (ACTIVE → EXPIRED).
7. **FX beyond Spec 3's model.** Whatever Spec 3 supports (rate lookup at transfer time, fallback rate table) is what we support. No bespoke OTC FX rules.
8. **Premium payable in shares.** Premium is always cash. No share-for-share OTC swaps.
9. **Encryption beyond TLS + HMAC.** Message payloads are not end-to-end encrypted beyond what Spec 3 provides. Inter-bank traffic is assumed to cross the same trust boundary as Spec 3's.
10. **Audit export / regulatory reporting.** The `inter_bank_saga_logs` and `InterBankSettlement` rows are audit-complete for our purposes; producing regulator reports is a separate surface.
11. **Mobile push batching / digests.** One Kafka event per saga terminal state maps to one push; no throttling or digesting.
12. **Negotiation history across a bank change.** If an offer's counterparty bank changes mid-negotiation (extraordinary), behaviour is undefined; the design assumes the counterparty bank is fixed at first engagement.
13. **Rate limiting per user.** Rate limits exist only at the bank-to-bank peer level (§7.5), not per end-user. Abuse prevention at the user level is an existing concern of api-gateway middleware.
14. **Observability dashboards for saga states.** Prometheus metrics on phase durations and failure rates are desirable but not specified here; basic logging is expected.

---

## 15. Open Questions and Risks

### 15.1 Blockers (must resolve before coding)

- **Spec 2 shipping.** This spec cannot be implemented until `OTCOffer`, `OptionContract`, `HoldingReservation` (keyed on contract), and intra-bank negotiation/accept/exercise flows exist. The implementation plan will begin with a go/no-go prerequisite check against Spec 2's delivered state.
- **Spec 3 shipping.** Without the HMAC transport, `Bank` registry, and 2PC transfer, cross-bank has no channel. Same prerequisite check as above.
- **Spec 3 `REVERSE_TRANSFER` addendum.** Section 6.2 above requires Spec 3 to be extended with reverse-transfer actions. If Spec 3's authors disagree, Phase 4 rollback becomes manual-reconciliation-only and the risk rating of this spec increases materially. **Recommendation:** fold the addendum into the Spec 3 implementation plan directly rather than adding it in a follow-up patch.

### 15.2 Known risks (non-blocking)

- **Phase 4 rollback when seller has already spent the premium.** Described in §6.1. Mitigation: escalate to ops via `otc.crossbank-saga-stuck-rollback`. Acceptance criterion: test case #6 in §12.2 covers this explicitly.
- **Clock skew between banks.** `settlement_date` is a date, not a timestamp; we use each bank's local midnight. If banks span time zones, the "who wakes first" for expiry might differ. Mitigation: compare in UTC; document that settlement is "end of settlement_date UTC". Add to spec doc if not already.
- **Discovery cache staleness during high-churn negotiations.** 60s TTL means two users might see slightly different offer states. Mitigation: `otc.local-offer-changed` Kafka-based bust. If that is lossy (Kafka retention expires), the 60s floor kicks in. Acceptable.
- **FX slippage between reserve and commit at buyer's bank.** Phase 1 reserves RSD at rate R1; Phase 3 commits at rate R2 if R1 ≠ R2. Mitigation: Phase 1 reserves a small buffer (1% default) above the expected commit amount; excess released at Phase 3. Details owned by Spec 3's FX model; flag here for cross-reference.
- **`Bank` registry synchronisation between peers.** Each bank independently maintains its `banks` table. If Bank A adds Bank C before Bank B does, and Bank A forwards an offer that references Bank C, Bank B cannot verify the forward chain. Mitigation: discovery is strict peer-to-peer (Bank A → Bank B), no forwarding; all cross-bank interactions are direct. No risk in current scope.
- **Actuary cross-bank visibility privacy.** Actuaries at Bank A see actuary offers from Bank B. Does Bank B's compliance team agree to share actuary identities across banks? Default: each offer carries an opaque `originator_reference` (hashed), not the real employee id. Accept.
- **Race between accept and expiry.** Offer's implicit expiry (offer's own `expires_at`, separate from contract `settlement_date`) passes between click and phase 1. Mitigation: Phase 2's peer validates `OTCOffer.status` still allows acceptance; if already expired, returns `OFFER_EXPIRED` and saga rolls back at phase-1-compensation.
- **Saga volume.** If cross-bank OTC becomes popular, `inter_bank_saga_logs` grows fast. Rotation policy: archive completed/compensated rows older than 180 days to a cold table. Out of scope for v1; note for future.

### 15.3 Non-risks we considered and dismissed

- "What if the peer bank's schema doesn't match ours?" — Both banks run the same codebase (faculty monorepo is deployed at every participating bank). Schema drift across deployments is out of scope; operational.
- "What if a client is deleted mid-saga?" — Client deletion is forbidden when they have active contracts (existing Spec 2 invariant). Cross-bank contracts extend that invariant: deletion requires no active cross-bank contracts on either side. Enforced by client-service pre-delete check.
- "What if the seller's share listing is delisted mid-contract?" — Listing delist flags the stock `inactive` but existing holdings and reservations are preserved (current behaviour). Cross-bank adds no new concern.

---

## Appendix A — Phase-by-phase state reference

The canonical state machine for a single cross-bank saga (`accept` variant), as tracked in `inter_bank_saga_logs`:

```
(each row: phase / role / status)

Bank A (initiator)          Bank B (responder)
─────────────────────       ──────────────────
p1 / initiator / pending    (no row yet)
p1 / initiator / completed  (no row yet)
p2 / initiator / pending    p2 / responder / pending
p2 / initiator / completed  p2 / responder / completed
p3 / initiator / pending    p3 / responder / pending
p3 / initiator / completed  p3 / responder / completed
p4 / initiator / pending    p4 / responder / pending
p4 / initiator / completed  p4 / responder / completed
p5 / initiator / pending    p5 / responder / pending
p5 / initiator / completed  p5 / responder / completed
```

Failure collapses the latest row to `failed` and earlier rows may transition to `compensating` → `compensated` as undo runs. No row is ever deleted; saga history is full audit.

---

## Appendix B — Illustrative message payloads

(Shape only, not actual wire format; JSON canonicalisation and HMAC signing per Spec 3.)

```
RESERVE_SHARES (phase 2 → peer):
{
  "tx_id": "a4f7...",
  "action": "RESERVE_SHARES",
  "saga_kind": "accept",
  "offer_id": "…",
  "asset_listing_id": "…",
  "quantity": 50,
  "buyer_bank_code": "A",
  "seller_bank_code": "B",
  "expected_settlement_date": "2025-04-05"
}

RESERVE_SHARES_CONFIRM (peer → initiator):
{
  "tx_id": "a4f7...",
  "action": "RESERVE_SHARES_CONFIRM",
  "shares_reserved": 50,
  "reservation_id": "…",
  "listing_snapshot": { "symbol": "AAPL", "lot_size": 1 }
}

TRANSFER_OWNERSHIP (phase 4 → peer):
{
  "tx_id": "a4f7...",
  "action": "TRANSFER_OWNERSHIP",
  "asset_listing_id": "…",
  "quantity": 50,
  "from_client_id": "…",
  "from_bank_code": "B",
  "to_client_id": "…",
  "to_bank_code": "A"
}

OWNERSHIP_CONFIRM (peer → initiator):
{
  "tx_id": "a4f7...",
  "action": "OWNERSHIP_CONFIRM",
  "assigned_at": "2025-04-03T14:22:11Z",
  "serial": "B-OTC-2025-0104"
}

FINAL_CONFIRM (phase 5 → peer):
{
  "tx_id": "a4f7...",
  "action": "FINAL_CONFIRM",
  "contract": { /* full snapshot */ }
}

CHECK_STATUS (recovery, either direction):
{
  "tx_id": "a4f7...",
  "action": "CHECK_STATUS",
  "phase": "transfer_ownership"   // optional; omit to return all phases
}

CHECK_STATUS_REPLY:
{
  "tx_id": "a4f7...",
  "phases": [
    { "phase": "reserve_buyer_funds", "role": "responder", "status": "n/a" },
    { "phase": "reserve_seller_shares", "role": "responder", "status": "completed" },
    { "phase": "transfer_funds", "role": "responder", "status": "completed" },
    { "phase": "transfer_ownership", "role": "responder", "status": "pending" },
    { "phase": "finalize", "role": "responder", "status": "n/a" }
  ]
}

REVERSE_TRANSFER_PREPARE (compensation after phase 4 fail):
{
  "tx_id": "rv-a4f7...",
  "action": "REVERSE_TRANSFER_PREPARE",
  "original_tx_id": "a4f7...",
  "amount": "1150.00",
  "currency": "USD",
  "memo": "crossbank-otc rollback {tx_id}"
}

CONTRACT_EXPIRE (expiry saga, phase 1 → peer):
{
  "tx_id": "ex-…",
  "action": "CONTRACT_EXPIRE",
  "contract_id": "…"
}
```

---

## Appendix C — Cross-reference index

- Celina 5 source: `docs/Celina 5(Nova).docx.md` lines 155–258 (Izvršavanje kupoprodaje), 262–285 (exercise examples), 130–157 (negotiation + acceptance narrative).
- Spec 2 design: `docs/superpowers/specs/2026-04-??-otc-options-intra-bank-design.md` (sibling in this series; to be landed).
- Spec 3 design: `docs/superpowers/specs/2026-04-??-inter-bank-2pc-transport-design.md` (sibling in this series; to be landed).
- Existing securities bank-safety spec: `docs/superpowers/specs/2026-04-22-securities-bank-safety-design.md` (removed in current branch but provides the saga-executor foundation this spec reuses).
- Intra-bank saga executor shipped code: `stock-service/internal/service/saga_executor.go`, `stock-service/internal/model/saga_log.go`, `stock-service/internal/repository/saga_log_repository.go`.
- Account-service idempotent money primitives: `account-service/internal/repository/ledger_repository.go` (`DebitWithLock`, `CreditWithLock`, `ReserveFunds`, `PartialSettleReservation`, `ReleaseReservation`).

— End of spec —
