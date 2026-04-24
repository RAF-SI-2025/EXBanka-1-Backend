# Intra-bank OTC Option Trading — Design Spec

## 1. Status / Date / Supersedes

- **Status:** Proposed (Phase 2 — Spec 2 of 4)
- **Date:** 2026-04-24
- **Author:** Platform / Securities working group
- **Branch:** `feature/securities-bank-safety`
- **Supersedes:** The direct-holding `OTC` flow currently implemented in `stock-service/internal/service/otc_service.go` (treated as **legacy**; see §3.3 for migration). The new semantics switch OTC trading on public stocks from an immediate-purchase model to an **option-contract-based** model, in line with "Celina 4 (Nova)" specification.
- **Succeeded by:** `docs/superpowers/specs/2026-04-25-crossbank-otc-options-design.md` (Spec 4) which extends the entities and sagas defined here to inter-bank OTC trading. This document defines the seams (see §3.7 and §4.5) that Spec 4 will plug into.

---

## 2. Motivation

Celina 4 ("Nova") introduces OTC trading explicitly as **option trading on stocks** — not as a direct spot purchase of another client's publicly listed holdings. The existing `otc_service.go` allows a buyer to atomically purchase shares held in "public mode" by another client; that semantics survives only for backward compatibility with integration tests that exercise it, but it will be deprecated in Spec 4.

The new flow mirrors a real over-the-counter options market:

1. A **seller** (who holds the underlying stock) writes an option, specifying:
   - the stock and quantity,
   - the **strike price** per share (the price at which the buyer may later buy),
   - the **premium** (the fee the buyer pays up-front to *own* the option),
   - the **settlement date** (the last date on which the option can be exercised).
2. The counterparty — the **buyer** — may **accept**, **counter-offer**, or **reject**. Negotiation is open-ended ("back and forth") until one side accepts or rejects.
3. On acceptance, an **option contract** is created, the **premium is paid immediately** buyer → seller, and the seller's shares are **reserved** against that contract (so they cannot be re-sold to another counterparty).
4. The buyer may then **exercise** the option any time up to the settlement date. Exercising moves both the strike-price funds (buyer → seller) and the shares (seller → buyer).
5. If the settlement date passes with no exercise, the option **expires**; the seller keeps the premium and the seller's reservation is released (shares become available again).

Key business justifications:

- **Reuses existing primitives.** Account-service already exposes `ReserveFunds / PartialSettleReservation / ReleaseReservation` with idempotency keys. Stock-service already has `HoldingReservationService`, `SagaLog`, and `SagaExecutor`. This spec wires these together; no new cross-service primitives required.
- **Bank-grade safety.** Every balance change (premium payment, strike payment, compensation) flows through the account-service ledger with a memo referencing the contract id. Every multi-step operation (accept, exercise) is a saga with deterministic idempotency keys.
- **Extensibility.** The entities carry nullable `*_bank_code` columns that are unset for intra-bank trades; Spec 4 will set them for cross-bank.

The "flow" section of Celina 4 (lines 127–164 of `docs/Celina 4(Nova).docx.md`) is the canonical source. The worked example there — Marija writes a 100-share option at 5 000 RSD strike with 50 000 RSD premium to Luka; if the market moves to 6 000 RSD, Luka exercises and profits 100 000 RSD net of premium; otherwise he abandons and loses only the premium — is the scenario our tests replay (see §10).

---

## 3. Architecture overview

### 3.1 Services involved

| Service | Role |
|---|---|
| **api-gateway** | HTTP entry, JWT, `system_type` forwarding, input validation, response shaping, swagger docs. |
| **stock-service** | Owns `OTCOffer`, `OTCOfferRevision`, `OptionContract`, `OTCOfferReadReceipt`. Owns the accept/exercise/expiry sagas. Extends `HoldingReservation` with `otc_contract_id`. |
| **account-service** | Funds movement: `ReserveFunds`, `PartialSettleReservation`, `ReleaseReservation`, `CreditAccount`, `DebitAccount`, ledger entries. |
| **exchange-service** | `Convert` RPC, used when buyer and seller accounts hold different currencies. |
| **notification-service** | Consumes `otc.*` kafka topics and delivers in-app/push notifications ("your offer was countered", "your option was exercised", "your option expired"). Out of scope of this spec beyond the topic definition (see §8). |
| **user-service / client-service** | Identity lookups (seller info panel on contract detail). |

### 3.2 Communication topology

```
               HTTP / JSON
                  │
           ┌──────▼──────┐
           │ api-gateway │
           └──────┬──────┘
                  │ gRPC
           ┌──────▼──────┐          ┌─────────────────┐
           │stock-service│──gRPC───▶│ account-service │
           └──┬────────┬─┘          └─────────────────┘
 Kafka (otc.*)│        │ gRPC
              ▼        ▼
  ┌───────────────┐  ┌────────────────┐
  │ notification  │  │ exchange-svc   │
  └───────────────┘  └────────────────┘
```

No direct HTTP from stock-service to other services; everything is gRPC. Kafka events are emitted **after** the originating DB transaction commits (per concurrency requirement in `CLAUDE.md`).

### 3.3 Legacy `otc_service.go` handling

The existing direct-purchase OTC code in `stock-service/internal/service/otc_service.go` is:

- **Retained** (unchanged routes) until Spec 4 ships, to avoid breaking integration tests `wf_otc_*.go` that depend on direct-purchase semantics.
- **Deprecated** via a log-warning on every call path (`logger.Warn("legacy direct-purchase OTC; migrate to option contracts")`).
- **Flagged** in `Specification.md` as "legacy; removal planned in Spec 4."

New endpoints (§7) live under `/api/v1/otc/offers/*` and `/api/v1/otc/contracts/*` — a separate namespace from the legacy `/api/v1/otc/listings/*` and `/api/v1/otc/purchase` routes. Gateway keeps both wired.

### 3.4 Reuse of existing infrastructure

| Primitive | Source | Used for |
|---|---|---|
| `SagaLog` + `SagaExecutor` | `stock-service/internal/saga/` | Orchestrating premium-payment saga and exercise saga. |
| `HoldingReservationService` | `stock-service/internal/service/holding_reservation_service.go` | Locking seller's shares per contract, keyed on new `otc_contract_id` column. |
| `ReserveFunds / PartialSettleReservation / ReleaseReservation` | account-service RPC | All premium and strike-price movements. |
| `GetReservation` | account-service RPC | Saga recovery — lets expiry cron check whether any funds are still reserved under a given idempotency key. |
| Ledger `Memo + IdempotencyKey` | account-service | Every balance change writes a ledger row with a deterministic memo referencing the contract id. |
| `system_type` forwarding | api-gateway middleware | Seller and buyer may each be `client` or `employee`; the JWT claim propagates into offer rows and all downstream RPCs. |

### 3.5 Idempotency key scheme

All saga steps use a deterministic key. The base string for the premium-payment saga is `otc-accept-{offer_id}`; for the exercise saga it is `otc-exercise-{contract_id}`. Each sub-operation appends a suffix:

| Operation | Key |
|---|---|
| Premium-reservation on buyer | `otc-accept-{offer_id}-reserve` |
| Premium-settlement debit on buyer | `otc-accept-{offer_id}-buyer` |
| Premium-settlement credit on seller | `otc-accept-{offer_id}-seller` |
| Compensating buyer credit | `otc-accept-{offer_id}-comp-buyer` |
| Compensating seller debit | `otc-accept-{offer_id}-comp-seller` |
| Exercise buyer reservation | `otc-exercise-{contract_id}-reserve` |
| Exercise buyer settlement | `otc-exercise-{contract_id}-buyer` |
| Exercise seller credit | `otc-exercise-{contract_id}-seller` |
| Exercise compensations | `otc-exercise-{contract_id}-comp-*` |

Suffixing guarantees that if a saga step is retried, account-service short-circuits on the idempotency key and returns the original result rather than double-debiting.

### 3.6 Transactional boundaries

The table below defines the DB transaction boundary for each saga. Critical rule: **the contract row and the holding reservation MUST be created in the same local DB transaction** — a dangling reservation with no contract would leak seller shares forever.

| Saga step | Scope |
|---|---|
| `validate_offer` | read-only |
| `reserve_seller_shares` + `create_contract` | **single local DB tx** in stock-service |
| `reserve_premium` | remote RPC to account-service (its own tx) |
| `settle_premium` | two remote RPCs, idempotent |
| `mark_offer_accepted` | single local DB tx in stock-service |
| `publish_kafka` | after all DB commits |

### 3.7 Cross-bank seams (Spec 4 hooks)

Fields added to entities that remain `NULL` in this spec and are wired up in Spec 4:

- `OTCOffer.counterparty_bank_code` `TEXT NULL` — NULL ⇒ intra-bank; non-NULL ⇒ cross-bank.
- `OTCOffer.initiator_bank_code` `TEXT NULL` — NULL ⇒ this bank is initiator.
- `OptionContract.seller_bank_code` / `buyer_bank_code` `TEXT NULL` — same convention.
- `OTCOffer.external_correlation_id` `UUID NULL` — populated by cross-bank gateway with the correlation id received from the foreign bank's BankRouter.

Saga steps in §6 have comments marking the "cross-bank fork" where Spec 4 inserts a federated RPC (`BankRouter.QuoteOffer`, `BankRouter.ExecuteExercise`) in place of a local account-service call. None of those forks are wired in this spec.

---

## 4. Data model

All new tables live in the **stock-service** schema. Column types use PostgreSQL syntax; `DECIMAL(20,8)` is the project's convention for monetary and share quantities.

### 4.1 `otc_offers`

```sql
CREATE TABLE otc_offers (
    id                          BIGSERIAL PRIMARY KEY,
    initiator_user_id           BIGINT       NOT NULL,
    initiator_system_type       TEXT         NOT NULL CHECK (initiator_system_type IN ('client','employee')),
    initiator_bank_code         TEXT             NULL,  -- Spec 4
    counterparty_user_id        BIGINT           NULL,  -- NULL = open offer
    counterparty_system_type    TEXT             NULL CHECK (counterparty_system_type IN ('client','employee')),
    counterparty_bank_code      TEXT             NULL,  -- Spec 4
    direction                   TEXT         NOT NULL CHECK (direction IN ('sell_initiated','buy_initiated')),
    stock_id                    BIGINT       NOT NULL REFERENCES stocks(id),
    quantity                    DECIMAL(20,8) NOT NULL CHECK (quantity > 0),
    strike_price                DECIMAL(20,8) NOT NULL CHECK (strike_price > 0),
    premium                     DECIMAL(20,8) NOT NULL CHECK (premium >= 0),
    settlement_date             DATE         NOT NULL,
    status                      TEXT         NOT NULL CHECK (status IN (
                                                 'PENDING','COUNTERED','ACCEPTED','REJECTED','EXPIRED','FAILED'
                                             )),
    last_modified_by_user_id        BIGINT NOT NULL,
    last_modified_by_system_type    TEXT   NOT NULL CHECK (last_modified_by_system_type IN ('client','employee')),
    external_correlation_id     UUID             NULL,  -- Spec 4
    created_at                  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    version                     BIGINT       NOT NULL DEFAULT 0
);
CREATE INDEX idx_otc_offers_initiator ON otc_offers(initiator_user_id, initiator_system_type, status);
CREATE INDEX idx_otc_offers_counterparty
    ON otc_offers(counterparty_user_id, counterparty_system_type, status)
    WHERE counterparty_user_id IS NOT NULL;
CREATE INDEX idx_otc_offers_stock_status ON otc_offers(stock_id, status);
```

Notes:
- `direction`:
  - `sell_initiated` — the **initiator** is the seller (posting an option they want to write).
  - `buy_initiated` — the initiator is the buyer (bidding on someone else's shares; the counterparty becomes the seller on acceptance).
- `version` is an optimistic-lock field with the standard `BeforeUpdate` hook from `CLAUDE.md`. Every `Save` on an offer checks `RowsAffected == 0` and returns `ErrOptimisticLock` on conflict.
- Status values beyond the requested ones: `FAILED` is used when the premium-payment saga cannot be compensated cleanly (stays for ops review). `EXPIRED` — see §5.1.

### 4.2 `otc_offer_revisions`

Append-only negotiation history — **never** updated in place.

```sql
CREATE TABLE otc_offer_revisions (
    id                          BIGSERIAL PRIMARY KEY,
    offer_id                    BIGINT NOT NULL REFERENCES otc_offers(id) ON DELETE CASCADE,
    revision_number             INT    NOT NULL,
    quantity                    DECIMAL(20,8) NOT NULL,
    strike_price                DECIMAL(20,8) NOT NULL,
    premium                     DECIMAL(20,8) NOT NULL,
    settlement_date             DATE   NOT NULL,
    modified_by_user_id         BIGINT NOT NULL,
    modified_by_system_type     TEXT   NOT NULL CHECK (modified_by_system_type IN ('client','employee')),
    action                      TEXT   NOT NULL CHECK (action IN ('CREATE','COUNTER','ACCEPT','REJECT')),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (offer_id, revision_number)
);
CREATE INDEX idx_otc_offer_revisions_offer ON otc_offer_revisions(offer_id);
```

`revision_number` starts at 1 on `CREATE`, monotonically increases per `COUNTER`, and is finalized on `ACCEPT` or `REJECT`. The final revision is what the contract is created from.

### 4.3 `option_contracts`

```sql
CREATE TABLE option_contracts (
    id                    BIGSERIAL PRIMARY KEY,
    offer_id              BIGINT       NOT NULL REFERENCES otc_offers(id),
    buyer_user_id         BIGINT       NOT NULL,
    buyer_system_type     TEXT         NOT NULL CHECK (buyer_system_type IN ('client','employee')),
    buyer_bank_code       TEXT             NULL,  -- Spec 4
    seller_user_id        BIGINT       NOT NULL,
    seller_system_type    TEXT         NOT NULL CHECK (seller_system_type IN ('client','employee')),
    seller_bank_code      TEXT             NULL,  -- Spec 4
    stock_id              BIGINT       NOT NULL REFERENCES stocks(id),
    quantity              DECIMAL(20,8) NOT NULL CHECK (quantity > 0),
    strike_price          DECIMAL(20,8) NOT NULL CHECK (strike_price > 0),
    premium_paid          DECIMAL(20,8) NOT NULL CHECK (premium_paid >= 0),
    premium_currency      TEXT         NOT NULL,       -- currency of the seller's account at accept time
    strike_currency       TEXT         NOT NULL,       -- currency of the seller's account at accept time
    settlement_date       DATE         NOT NULL,
    status                TEXT         NOT NULL CHECK (status IN ('ACTIVE','EXERCISED','EXPIRED','FAILED')),
    saga_id               UUID         NOT NULL,       -- premium-payment saga id; exercise saga id stored in saga_log
    premium_paid_at       TIMESTAMPTZ  NOT NULL,
    exercised_at          TIMESTAMPTZ      NULL,
    expired_at            TIMESTAMPTZ      NULL,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    version               BIGINT       NOT NULL DEFAULT 0,
    UNIQUE (offer_id)
);
CREATE INDEX idx_option_contracts_buyer  ON option_contracts(buyer_user_id, buyer_system_type, status);
CREATE INDEX idx_option_contracts_seller ON option_contracts(seller_user_id, seller_system_type, status);
CREATE INDEX idx_option_contracts_settlement ON option_contracts(settlement_date) WHERE status = 'ACTIVE';
```

Notes:
- `UNIQUE (offer_id)` — at most one contract per accepted offer.
- `strike_currency` is the **seller's** account currency at acceptance time. This is what the buyer must pay in (after optional conversion from buyer's account currency at exercise time).
- `premium_currency` is also the seller's account currency. The buyer pays the premium in their own currency and we convert via exchange-service before crediting the seller.
- `version` — standard optimistic lock.
- `saga_id` is stored for forensic correlation with the saga log; the exercise saga id lives in `saga_log.correlation_id` keyed on contract id.

### 4.4 `otc_offer_read_receipts`

```sql
CREATE TABLE otc_offer_read_receipts (
    user_id               BIGINT NOT NULL,
    system_type           TEXT   NOT NULL CHECK (system_type IN ('client','employee')),
    offer_id              BIGINT NOT NULL REFERENCES otc_offers(id) ON DELETE CASCADE,
    last_seen_updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, system_type, offer_id)
);
```

Semantics:
- Inserted / upserted on every `GET /api/v1/otc/offers/{id}` for the calling user.
- On offer-list queries (`GET /api/v1/me/otc/offers`), a `LEFT JOIN` computes `unread = (offers.updated_at > coalesce(receipt.last_seen_updated_at, '-infinity'))`.

### 4.5 Extension of `holding_reservations`

The existing table is extended with an `otc_contract_id` column and a CHECK constraint ensuring **exactly one** of `order_id` or `otc_contract_id` is set per row:

```sql
ALTER TABLE holding_reservations
  ADD COLUMN otc_contract_id BIGINT NULL REFERENCES option_contracts(id);

ALTER TABLE holding_reservations
  ADD CONSTRAINT holding_reservation_exactly_one_target
  CHECK ( (order_id IS NULL) <> (otc_contract_id IS NULL) );

CREATE INDEX idx_holding_reservations_contract
  ON holding_reservations(otc_contract_id)
  WHERE otc_contract_id IS NOT NULL;
```

Semantics:
- An OTC-contract reservation holds the **seller's** shares from `premium_paid_at` until either the buyer exercises or the contract expires.
- On exercise: the reservation is **consumed** (decrement `holding.quantity` and `holding.reserved_quantity` both by `contract.quantity`, delete the reservation row).
- On expiry: the reservation is **released** (decrement `holding.reserved_quantity` by `contract.quantity`, delete the reservation row; `holding.quantity` unchanged).
- `available_quantity` is a derived expression: `holding.quantity - holding.reserved_quantity`.

### 4.6 Seller concurrent-contract invariant

Per Celina 4: a seller may have any number of simultaneous offers and contracts on the same stock, **provided all are simultaneously satisfiable**. Concretely, for any `(seller_user_id, seller_system_type, stock_id)`:

```
SUM(active_reservations.quantity) ≤ holding.quantity
```

where `active_reservations` includes:
- `OptionContract` rows with status `ACTIVE`, **plus**
- `OTCOffer` rows with status `PENDING`/`COUNTERED` **AND** `direction='sell_initiated'` where the initiator is the seller, **plus**
- `OTCOffer` rows with status `PENDING`/`COUNTERED` **AND** `direction='buy_initiated'` where the counterparty is the seller and the seller has not rejected.

**Enforcement mechanics:**

1. **On create-offer** (sell_initiated):
   - Open a DB tx.
   - `SELECT … FOR UPDATE` on the seller's `holdings` row.
   - Compute "committed quantity" as the sum above.
   - If `holding.quantity - committed < requested.quantity` → return `FailedPrecondition("insufficient available shares for this seller")`.
   - Insert the offer.
   - Commit.
2. **On create-offer** (buy_initiated): no check on the **initiator** (they are buying); on the counterparty (seller), the same check runs when that counterparty first *responds* (accepts or counters — the moment they commit to the deal). Before that, multiple buyers may bid on the same seller's shares concurrently; the first to be accepted wins.
3. **On accept**: inside the premium-payment saga, step `reserve_seller_shares` re-runs the check with `SELECT FOR UPDATE` on the seller's holding. If the check fails, the saga aborts with `FailedPrecondition("shares no longer available")` and no side effects are created.
4. **On counter**: treated as an edit of the same offer; no new reservation is added, but the quantity delta is re-validated against the seller's available quantity.

This is the "last writer loses" model, per Open Question #4.

---

## 5. State machines

### 5.1 Offer lifecycle

```
                                 ┌────────┐
                                 │CREATED │     (POST /otc/offers)
                                 └───┬────┘
                                     │
                                ┌────▼────┐
               counter◀─────────│PENDING  │─────────▶ reject
                    │           └────┬────┘                │
               create new          accept                  │
               revision;             │                     │
               status=COUNTERED      │                     │
                    │          ┌─────▼─────┐               │
                    ▼          │ running   │               │
              ┌───────────┐    │ accept    │               ▼
              │COUNTERED  │───▶│ saga      │          ┌─────────┐
              └─────┬─────┘    └─────┬─────┘          │REJECTED │
                    │                │                └─────────┘
            counter/accept/reject    │ saga ok
                                     ▼
                               ┌──────────┐
                               │ ACCEPTED │  ⇒ OptionContract row created
                               └──────────┘

Time-based transition (daily cron, see §6.3):
  PENDING or COUNTERED with settlement_date < today  ⇒  EXPIRED
Saga-failure transition:
  PENDING/COUNTERED + unrecoverable saga failure      ⇒  FAILED
```

Terminal states: `ACCEPTED`, `REJECTED`, `EXPIRED`, `FAILED`. Once terminal, the offer is **immutable** (no counters, no re-open).

**Allowed transitions table:**

| From | Action | By | To |
|---|---|---|---|
| — (insert) | `CREATE` | initiator | `PENDING` |
| `PENDING` / `COUNTERED` | `COUNTER` | counterparty or initiator (the "other side" from last_modified_by) | `COUNTERED` |
| `PENDING` / `COUNTERED` | `ACCEPT` | the side that did NOT last modify | `ACCEPTED` (via saga) |
| `PENDING` / `COUNTERED` | `REJECT` | either side | `REJECTED` |
| `PENDING` / `COUNTERED` | cron: `settlement_date < today` | system | `EXPIRED` |
| any | accept-saga unrecoverable failure | system | `FAILED` |

**Last-mover rule:** the side that `last_modified_by` points to cannot be the one to `ACCEPT` — they already stated their terms; the *other* side decides. (Prevents auto-accept-your-own-counter.)

### 5.2 Contract lifecycle

```
         ┌────────┐
         │CREATED │  (inside premium-payment saga, inside same local tx as holding reservation)
         └───┬────┘
             │ saga step 5 commit
         ┌───▼────┐
         │ ACTIVE │◀──── reservation on seller's holding is live
         └───┬────┘
             │
     ┌───────┼────────────────────┐
     ▼       ▼                    ▼
 exercise  settlement_date     saga error
 (buyer)   passes              during accept
     │       │                    │
     ▼       ▼                    ▼
┌──────────┐┌─────────┐      ┌────────┐
│EXERCISED ││ EXPIRED │      │ FAILED │
└──────────┘└─────────┘      └────────┘
```

- `EXERCISED` — shares and strike funds moved; reservation consumed.
- `EXPIRED` — cron-driven; reservation released; premium stays with seller.
- `FAILED` — only reachable when a premium-payment saga partially commits and compensations themselves fail; left for manual ops reconciliation. The seller's reservation in this case stays until ops resolves.

Terminal states: `EXERCISED`, `EXPIRED`, `FAILED`. All immutable.

### 5.3 Read-receipt lifecycle

```
GET /otc/offers/{id} by user U with system_type S
    UPSERT otc_offer_read_receipts
        (user_id=U, system_type=S, offer_id=ID)
        SET last_seen_updated_at = GREATEST(existing, offers.updated_at)
```

List endpoint calculates `unread` lazily by comparing `offers.updated_at` with the receipt row (or absence thereof). No cron, no race; strictly a derived view.

---

## 6. Saga breakdowns

All sagas use `SagaExecutor` and write one `saga_log` row per step.

### 6.1 Premium-payment saga (on accept)

**Entry:** `POST /api/v1/otc/offers/{id}/accept` by buyer.

**Preconditions (validated in step 1 before saga execution starts):**
- Offer exists and status in {`PENDING`, `COUNTERED`}.
- Caller is the "other side" from last_modified_by (§5.1 last-mover rule).
- Caller identity resolves to the designated buyer (based on direction):
  - direction=`sell_initiated` ⇒ buyer is the **counterparty** (or any caller if counterparty is NULL — open offer).
  - direction=`buy_initiated` ⇒ buyer is the **initiator**; the accepting side (caller) must be the counterparty (seller).
- Settlement date > today.

**Saga steps:**

| # | Step | Service | Action | Compensation |
|---|---|---|---|---|
| 1 | `validate_offer` | stock | Re-check preconditions, resolve buyer & seller account ids (primary account per currency, see §6.4). | — |
| 2 | `reserve_seller_shares` + `create_contract` | stock | **Single local DB tx:** SELECT FOR UPDATE on seller's holding; recompute invariant (§4.6); insert `option_contracts` row with status=ACTIVE; insert `holding_reservations` row with `otc_contract_id=new.id`, `quantity=offer.quantity`; update `holdings.reserved_quantity += offer.quantity`. | Local tx: delete reservation, delete contract, restore holding reservation. |
| 3 | `reserve_premium` | account | `ReserveFunds(buyer_account, premium_in_buyer_currency, idem="otc-accept-{offer_id}-reserve")`. Premium in buyer currency = `exchange.Convert(seller_currency → buyer_currency, premium)`. | `ReleaseReservation(idem="otc-accept-{offer_id}-reserve")`. |
| 4 | `settle_premium_buyer` | account | `PartialSettleReservation(idem="otc-accept-{offer_id}-buyer", amount=premium_in_buyer_currency, memo="OTC premium for contract #{contract_id}")`. Writes buyer-side ledger entry. | `CreditAccount(buyer_account, amount, memo="Compensating OTC premium #{contract_id}", idem="otc-accept-{offer_id}-comp-buyer")`. |
| 5 | `settle_premium_seller` | account | `CreditAccount(seller_account, premium_in_seller_currency, memo="OTC premium credit for contract #{contract_id}", idem="otc-accept-{offer_id}-seller")`. | `DebitAccount(seller_account, premium_in_seller_currency, memo="Compensating OTC premium credit #{contract_id}", idem="otc-accept-{offer_id}-comp-seller")`. |
| 6 | `mark_offer_accepted` | stock | Local tx: `UPDATE otc_offers SET status='ACCEPTED', updated_at=now(), version=version+1 WHERE id=? AND version=?`. On RowsAffected=0 → optimistic-lock error → trigger compensation. Append final `otc_offer_revisions` row with `action='ACCEPT'`. | `UPDATE otc_offers SET status='PENDING'/'COUNTERED'` (restore prior status). Append revision row noting compensation. |
| 7 | `publish_kafka` | stock | After commit: emit `otc.contract-created` with contract, offer, buyer/seller ids, premium amount. | On publish failure: **do not roll back**; enqueue for retry (kafka is consumer-driven; events are the "outbox" side). |

**Recovery on crash:** On stock-service startup, `SagaExecutor.ResumePending()` scans `saga_log` for rows with status `pending`/`compensating` and continues them. Each step's idempotency key means reruns converge to the original result.

**Unrecoverable failure:** If a compensation itself fails after N retries, the contract moves to `FAILED` and the seller's reservation stays; ops has a Grafana alert on `status=FAILED`.

### 6.2 Exercise saga

**Entry:** `POST /api/v1/otc/contracts/{id}/exercise` by buyer.

**Preconditions (step 1):**
- Contract exists and status=`ACTIVE`.
- `settlement_date >= today`.
- Caller is the buyer (`buyer_user_id`, `buyer_system_type` match JWT).

**Saga steps:**

| # | Step | Service | Action | Compensation |
|---|---|---|---|---|
| 1 | `validate_contract` | stock | Re-check preconditions. Resolve buyer's and seller's primary accounts. | — |
| 2 | `compute_amounts` | stock + exchange | Native amount = `strike_price × quantity` in seller's `strike_currency`. If buyer account currency ≠ `strike_currency`, call `exchange.Convert`; record converted amount on the saga log for audit. | — (stateless). |
| 3 | `reserve_buyer_funds` | account | `ReserveFunds(buyer_account, converted_amount, idem="otc-exercise-{contract_id}-reserve")`. | `ReleaseReservation(idem="otc-exercise-{contract_id}-reserve")`. |
| 4 | `partial_settle_buyer` | account | `PartialSettleReservation(idem="otc-exercise-{contract_id}-buyer", amount=converted_amount, memo="Exercise option #{contract_id}")` — debits buyer. | `CreditAccount(buyer_account, converted_amount, memo="Compensating exercise option #{contract_id}", idem="otc-exercise-{contract_id}-comp-buyer")`. |
| 4b | `credit_seller` | account | `CreditAccount(seller_account, native_amount, memo="Exercise proceeds for option #{contract_id}", idem="otc-exercise-{contract_id}-seller")`. | `DebitAccount(seller_account, native_amount, memo="Compensating exercise proceeds #{contract_id}", idem="otc-exercise-{contract_id}-comp-seller")`. |
| 5 | `transfer_shares` | stock | **Single local DB tx:** SELECT FOR UPDATE on seller's holding and the reservation row; decrement `holdings.quantity -= contract.quantity` AND `holdings.reserved_quantity -= contract.quantity` on seller; upsert buyer's holding row with `quantity += contract.quantity`; delete the `holding_reservations` row for this contract; insert an audit `order_transactions`-equivalent row marked as `source='otc_exercise'`. | Local tx: reverse: seller.quantity += q, seller.reserved += q (re-insert reservation row); buyer.quantity -= q; delete audit row. |
| 6 | `mark_contract_exercised` | stock | `UPDATE option_contracts SET status='EXERCISED', exercised_at=now(), version+1 WHERE id=? AND version=?`. On RowsAffected=0 → optimistic-lock error. | `UPDATE option_contracts SET status='ACTIVE', exercised_at=NULL, version+1`. |
| 7 | `publish_kafka` | stock | After commit: emit `otc.contract-exercised`. | On failure: enqueue retry; do not roll back. |

**Compensation cascade matrix** (restating the requirement in tabular form — what runs when step N fails):

| Step that fails | Compensation steps executed (in reverse order) |
|---|---|
| 1 | none |
| 2 | none |
| 3 | none (no funds yet reserved) |
| 4 | `release reservation` (step 3 comp) |
| 4b | `debit seller` (step 4b comp), `credit buyer` (step 4 comp), `release reservation` (step 3 comp) |
| 5 | `debit seller`, `credit buyer`, `release reservation` |
| 6 | `undo share transfer` (step 5 comp), `debit seller`, `credit buyer`, `release reservation` |
| 7 | **no compensation** — trade is valid; kafka delivery is retried. |

### 6.3 Expiry cron

**Schedule:** daily at 02:00 UTC (configurable via `OTC_EXPIRY_CRON_UTC`).

**Work units:**

Unit A — expire contracts:
```
SELECT * FROM option_contracts
 WHERE status = 'ACTIVE' AND settlement_date < CURRENT_DATE
 ORDER BY id
 FOR UPDATE SKIP LOCKED
 LIMIT batch_size
```
For each:
1. Open tx, SELECT FOR UPDATE on contract and on the matching `holding_reservations` row.
2. Update holding: `reserved_quantity -= contract.quantity`. (Quantity unchanged — seller keeps shares AND premium.)
3. Delete `holding_reservations` row.
4. `UPDATE option_contracts SET status='EXPIRED', expired_at=now(), version+1`.
5. Commit.
6. Publish `otc.contract-expired`.

Unit B — expire offers:
```
SELECT * FROM otc_offers
 WHERE status IN ('PENDING','COUNTERED') AND settlement_date < CURRENT_DATE
 ORDER BY id
 FOR UPDATE SKIP LOCKED
 LIMIT batch_size
```
For each:
1. `UPDATE otc_offers SET status='EXPIRED', updated_at=now(), version+1`.
2. Publish `otc.offer-expired`.

No reservation is held at the offer level (only contracts carry reservations), so nothing to release in Unit B.

**Concurrency safety:** `FOR UPDATE SKIP LOCKED` + batched pagination. The goroutine accepts `context.Context` and honors cancellation (§CLAUDE.md concurrency rules); `defer ticker.Stop()`; `select { case <-time.After(interval): ... case <-ctx.Done(): return }`.

**Recovery after missed runs:** Cron is idempotent — `status='ACTIVE' AND settlement_date < CURRENT_DATE` catches everything missed since the last successful run.

### 6.4 Account selection rule

Given a user (user_id, system_type) and a target currency, the user's "account for this currency" is:

1. The **primary** account flagged in client-service / user-service for that currency, if any.
2. Otherwise the **first active** account of that currency in creation order.
3. Otherwise → `FailedPrecondition("no account in currency X for user")`.

Buyer's working currency is their primary RSD account for premium payment if they have one; else the first active account of any currency (converting as needed). The contract stores the **seller's** currency for `premium_currency` and `strike_currency` because those are the amounts the seller expects to net.

---

## 7. REST API surface

All routes are under the API gateway at `/api/v1/`. Swagger annotations must be updated on every handler (per `CLAUDE.md`). All request bodies pass through gateway validation (§CLAUDE.md API Gateway Input Validation Requirement). All error responses use the `apiError()` helper.

### 7.1 `POST /api/v1/otc/offers` — create offer

**Auth:** `AnyAuthMiddleware` + `RequirePermission("otc.trade")`.

**Body:**
```json
{
  "direction": "sell_initiated",
  "stock_id": 42,
  "quantity": "100",
  "strike_price": "5000.00",
  "premium": "50000.00",
  "settlement_date": "2026-05-10",
  "counterparty_user_id": 87,
  "counterparty_system_type": "client"
}
```

Validation (gateway):
- `direction` ∈ {`sell_initiated`, `buy_initiated`} (case-insensitive via `oneOf()`).
- `quantity`, `strike_price`, `premium` are decimals, positive (premium may be 0).
- `settlement_date` ≥ today + 1 day.
- `counterparty_*` either both present or both absent. If absent, direction must be `sell_initiated` (an "open offer" to any buyer). `buy_initiated` with no counterparty is rejected — bidding into the void is disallowed.
- `counterparty_system_type` ∈ {`client`, `employee`}.
- Initiator identity is taken from JWT (never from body).

**Response (201):**
```json
{
  "id": 101,
  "direction": "sell_initiated",
  "stock_id": 42,
  "stock_ticker": "AAPL",
  "quantity": "100",
  "strike_price": "5000.00",
  "premium": "50000.00",
  "settlement_date": "2026-05-10",
  "status": "PENDING",
  "initiator": { "user_id": 55, "system_type": "client" },
  "counterparty": { "user_id": 87, "system_type": "client" },
  "last_modified_by": { "user_id": 55, "system_type": "client" },
  "market_reference_price": "4950.00",
  "created_at": "2026-04-24T10:01:02Z",
  "updated_at": "2026-04-24T10:01:02Z",
  "version": 0,
  "unread": false
}
```

`market_reference_price` is `listing.high` (current ask) — included so the client can apply the color bands (green ±5%, yellow 5–20%, red >20%) without a second round-trip.

Error codes:
- 400 `validation_error` (bad enum, negative number, etc.).
- 403 `forbidden` (missing `otc.trade`).
- 409 `business_rule_violation` — insufficient seller shares (§4.6 check) or invalid date.

### 7.2 `GET /api/v1/me/otc/offers`

**Auth:** `AnyAuthMiddleware`.

**Query params:**
- `role` ∈ {`initiator`, `counterparty`, `either`} (default `either`).
- `status` ∈ {`PENDING`, `COUNTERED`, `ACCEPTED`, `REJECTED`, `EXPIRED`, `FAILED`} — optional, repeated values OR'd.
- `stock_id` — optional.
- Pagination: `page`, `page_size` (default 20, max 100).

Only **one** of `role` or `stock_id` filters may be combined with status; supplying both narrow filters at once is fine (they AND). Returning 400 on conflicting/ambiguous filter combinations follows the existing REST convention for single-filter endpoints.

**Response shape:** array of offer objects per §7.1 plus a per-item `unread: bool` (computed via join with `otc_offer_read_receipts`).

### 7.3 `GET /api/v1/otc/offers/{id}`

**Auth:** `AnyAuthMiddleware` + caller must be initiator or counterparty (403 otherwise). Employees with `otc.manage` (admin override — Spec 3 adds this; in this spec use `otc.trade` as the gate) may read any offer.

**Side effect:** UPSERT into `otc_offer_read_receipts` for the caller with `last_seen_updated_at = offer.updated_at`.

**Response:** full offer object (per §7.1) plus:
```json
"revisions": [
  { "revision_number": 1, "quantity": "100", "strike_price": "5000.00", "premium": "50000.00",
    "settlement_date": "2026-05-10", "action": "CREATE",
    "modified_by": {"user_id":55,"system_type":"client"},
    "created_at": "2026-04-24T10:01:02Z"
  },
  ...
]
```

### 7.4 `POST /api/v1/otc/offers/{id}/counter`

**Body:**
```json
{
  "quantity": "80",
  "strike_price": "4800.00",
  "premium": "45000.00",
  "settlement_date": "2026-05-10"
}
```

Validation:
- Offer must be in {`PENDING`,`COUNTERED`}.
- Caller must be the side that did NOT last modify (last-mover rule).
- Seller-side invariant re-checked if the quantity changed.
- All four fields required (a counter is a full re-statement, not a partial patch).

**Action:** atomic DB tx — insert new `otc_offer_revisions` row with `action='COUNTER'` and next `revision_number`, update `otc_offers` (status=`COUNTERED`, fields overwritten with the counter values, `last_modified_by_*` = caller, `updated_at=now()`, version+1).

**Response (200):** updated offer object.

### 7.5 `POST /api/v1/otc/offers/{id}/accept`

**No body.**

**Action:** runs the premium-payment saga (§6.1). Synchronous — the HTTP call waits for the saga to reach a terminal state (ACCEPTED or FAILED) or times out (60s) and returns 202 with a saga-id for polling (fallback path — see §6.1 recovery).

**Response (201):**
```json
{
  "offer_id": 101,
  "contract_id": 7,
  "status": "ACCEPTED",
  "saga_id": "abc-123",
  "contract": { ...full contract object as §7.8... }
}
```

**Errors:**
- 409 `conflict` — offer not in accept-eligible status.
- 409 `business_rule_violation` — insufficient shares at accept time, insufficient buyer funds, date passed.
- 503 `saga_in_progress` — saga accepted but still running after timeout; returned with `saga_id` to poll.

### 7.6 `POST /api/v1/otc/offers/{id}/reject`

**No body.** Caller must be initiator or counterparty. Offer must be in {`PENDING`,`COUNTERED`}. Transitions status to `REJECTED` (single DB tx, append final revision row with `action='REJECT'`).

**Response (200):** updated offer object.

### 7.7 `GET /api/v1/me/otc/contracts`

**Auth:** `AnyAuthMiddleware`.

**Query params:** `status` ∈ {`ACTIVE`,`EXERCISED`,`EXPIRED`,`FAILED`} (optional), `role` ∈ {`buyer`,`seller`,`either`} (default `either`), pagination.

**Response:** array of contract objects.

### 7.8 `GET /api/v1/otc/contracts/{id}`

**Auth:** `AnyAuthMiddleware`; caller must be buyer or seller (or employee with `otc.trade`).

**Response:**
```json
{
  "id": 7,
  "offer_id": 101,
  "stock_id": 42,
  "stock_ticker": "AAPL",
  "quantity": "100",
  "strike_price": "5000.00",
  "premium_paid": "50000.00",
  "premium_currency": "RSD",
  "strike_currency": "RSD",
  "settlement_date": "2026-05-10",
  "status": "ACTIVE",
  "buyer": { "user_id": 55, "system_type": "client", "display_name": "Luka L." },
  "seller": { "user_id": 87, "system_type": "client", "display_name": "Marija M." },
  "market_reference_price": "6000.00",
  "profit_if_exercised": {
     "strike_cost": "500000.00",
     "market_value": "600000.00",
     "premium_paid": "50000.00",
     "net_profit": "50000.00",
     "currency": "RSD"
  },
  "premium_paid_at": "2026-04-24T10:05:07Z",
  "exercised_at": null,
  "expired_at": null,
  "created_at": "2026-04-24T10:05:07Z",
  "updated_at": "2026-04-24T10:05:07Z",
  "version": 0
}
```

`profit_if_exercised` is derived: `market_value - strike_cost - premium_paid` in the seller's `strike_currency`, where `market_value = listing.high × quantity`. If `listing.high` is unknown, field is omitted.

### 7.9 `POST /api/v1/otc/contracts/{id}/exercise`

**No body.** Auth: caller must be buyer (JWT matches `buyer_user_id`/`buyer_system_type`). Runs exercise saga (§6.2) synchronously with timeout → 202/503 fallback (mirrors accept).

**Response (200):**
```json
{
  "contract_id": 7,
  "status": "EXERCISED",
  "saga_id": "def-456",
  "amounts": {
    "strike_amount_seller_ccy": "500000.00",
    "strike_amount_buyer_ccy": "500000.00",
    "ccy_rate_used": "1.0",
    "seller_currency": "RSD",
    "buyer_currency": "RSD"
  },
  "shares_transferred": "100"
}
```

**Errors:**
- 403 `forbidden` — not the buyer.
- 409 `business_rule_violation` — not active, settlement passed, insufficient buyer funds.
- 503 `saga_in_progress` — see §7.5.

### 7.10 Gateway client wiring

- New gRPC client in api-gateway: `otcClient` (reuses the existing stock-service conn).
- New handler file: `api-gateway/internal/handler/otc_options_handler.go`.
- New router block: `api-gateway/internal/router/otc_options.go` registered from `router.Register(...)`.
- Validation helpers: reuse `oneOf()`, `positive()`, `validateDate()`; add `validateFutureDate()` for `settlement_date`.

---

## 8. Kafka topics

All topic names follow the `<service>.<event>` convention. Message structs live in `contract/kafka/messages.go` (one struct per topic; JSON-serialized). All topics are pre-created via `EnsureTopics` in stock-service AND notification-service (CLAUDE.md requirement).

| Topic | Producer | Consumer(s) | Payload (fields) |
|---|---|---|---|
| `otc.offer-created` | stock | notification | `offer_id`, `initiator`, `counterparty`, `stock_id`, `quantity`, `strike_price`, `premium`, `settlement_date`, `created_at` |
| `otc.offer-countered` | stock | notification | `offer_id`, `revision_number`, `modified_by`, `counterparty_of_modifier`, `new_terms{...}`, `updated_at` |
| `otc.offer-rejected` | stock | notification | `offer_id`, `rejected_by`, `other_party`, `updated_at` |
| `otc.offer-expired` | stock | notification | `offer_id`, `initiator`, `counterparty`, `expired_at` |
| `otc.contract-created` | stock | notification | `contract_id`, `offer_id`, `buyer`, `seller`, `quantity`, `strike_price`, `premium_paid`, `settlement_date`, `premium_paid_at` |
| `otc.contract-exercised` | stock | notification | `contract_id`, `buyer`, `seller`, `strike_amount_paid`, `shares_transferred`, `exercised_at` |
| `otc.contract-expired` | stock | notification | `contract_id`, `buyer`, `seller`, `expired_at` |
| `otc.contract-failed` | stock | notification + ops | `contract_id`, `failure_reason`, `saga_id`, `compensation_status` |

**Publishing discipline:** every topic is emitted **after** the originating DB transaction commits (CLAUDE.md concurrency rule). The saga wrapper handles this automatically; outside the saga (e.g., cron), the pattern is `tx.Commit(); if err := producer.Publish(...); err != nil { log.Warn(...) }`.

**Notification-service consumer mappings** (additive — Spec 3 may refine):
- `otc.offer-created` (when counterparty ≠ NULL) → push to counterparty user.
- `otc.offer-countered` → push to the "other side" (new read receipt stays stale → `unread:true` on list).
- `otc.contract-created` → push to both parties.
- `otc.contract-exercised` → push to seller (buyer is the actor, they already know).
- `otc.contract-expired` → push to buyer (they missed their window).
- `otc.contract-failed` → internal ops alert channel.

---

## 9. Permissions

New permission: **`otc.trade`**.

**Granted by default to:**
- `EmployeeAgent` (can trade securities).
- `EmployeeSupervisor`.
- `EmployeeAdmin` (inherits all).
- All clients with `securities.trade` — granted implicitly at client creation time. (Per the existing pattern in `client-service`, client permissions are not stored in a `permissions` table; they're inferred from the client's securities-enabled flag. The gateway's `RequirePermission` helper recognizes this flag as conferring `otc.trade`.)

**Not granted to:** `EmployeeBasic`.

**Seeding:** update `user-service/internal/service/role_service.go`'s `EmployeeAgent`, `EmployeeSupervisor`, `EmployeeAdmin` default-permissions list to include `otc.trade`.

**Existing `securities.trade` is a prerequisite** — gateway-level handler runs `requireAll("securities.trade","otc.trade")` rather than just `RequirePermission("otc.trade")`. Rationale: OTC options are a securities-trading activity; anyone whose securities.trade has been revoked must also lose OTC access.

**New supervisor-only capability (future, §13 Open Q #1):** `otc.exercise-on-behalf` — would allow a supervisor to exercise on behalf of a fund. **Not introduced in this spec**; default buyer-only rule applies.

---

## 10. Testing strategy

### 10.1 Unit tests

Per CLAUDE.md, every service change requires unit tests on both the service and handler layers.

**stock-service/internal/service:**
- `otc_offer_service_test.go`:
  - create-offer happy path (sell_initiated, with and without counterparty).
  - create-offer validation fails (zero quantity, past settlement_date, bad direction).
  - create-offer fails seller-invariant (mock holding with no available shares).
  - counter happy path; counter by the same side (last-mover) rejected; counter on REJECTED offer rejected.
  - reject happy path; reject on ACCEPTED offer rejected.
- `otc_accept_saga_test.go`:
  - happy path with mocked account-service + exchange-service.
  - failure at step 3 (reserve funds) — verifies no contract row, no reservation.
  - failure at step 5 (credit seller) — verifies buyer is re-credited and reservation released; contract row rolled back.
  - failure at step 6 (mark accepted, optimistic-lock) — verifies full compensation.
  - idempotency: re-invoke accept with same offer id (in-flight) — returns original saga id.
- `otc_exercise_saga_test.go`:
  - happy path (same-currency and cross-currency).
  - step 4 fail → full release, no ledger changes committed net.
  - step 5 fail → seller debit reversed, buyer re-credited, reservation restored, shares restored.
  - step 6 fail → same + shares undone.
  - exercise on `EXPIRED` contract → `FailedPrecondition`.
  - exercise on contract where buyer ≠ caller → `PermissionDenied`.
- `otc_expiry_cron_test.go`:
  - contract with `settlement_date < today` transitions to EXPIRED; reservation deleted; `holding.quantity` unchanged, `reserved_quantity` decremented.
  - offer with `settlement_date < today` transitions to EXPIRED.
  - cron is idempotent on a second run — no double-free.
  - cron honors `context.Done()` mid-batch.

**stock-service/internal/handler:** standard gRPC handler tests — happy path + each error mapping (NotFound, FailedPrecondition, PermissionDenied, AlreadyExists for duplicate acceptance).

**api-gateway/internal/handler:**
- `otc_options_handler_test.go`:
  - create-offer validation (bad enum, bad numbers, missing counterparty fields paired with buy_initiated).
  - accept, reject, counter map gRPC errors correctly (403, 409).
  - `/api/v1/me/otc/offers` correctly extracts caller from JWT and passes `user_id` + `system_type` to gRPC list call; never trusts URL-supplied user ids.
  - read-receipt side effect occurs on `GET /otc/offers/{id}`.

### 10.2 Integration tests (minimum 8)

Under `test-app/workflows/`, file prefix `wf_otc_options_*.go`. Use `test-app/workflows/helpers_test.go` + `contract/testutil/` shared helpers; do NOT inline Kafka scans or verification flows.

1. **`wf_otc_options_happy_path_test.go`** — Seller writes offer, buyer accepts, buyer exercises before settlement. Asserts: premium moved at accept (ledger entries on both sides); strike moved at exercise; shares transferred; offer/contract final statuses; 4 kafka events (`otc.offer-created`, `otc.contract-created`, `otc.contract-exercised`, plus any notification acks).
2. **`wf_otc_options_expire_unexercised_test.go`** — Accept an offer with settlement_date = today. Advance test clock by 1 day via helper. Run expiry cron. Assert contract → `EXPIRED`, seller keeps premium, seller shares return to available, `otc.contract-expired` emitted. Buyer balance unchanged net of premium.
3. **`wf_otc_options_counter_flow_test.go`** — Seller creates offer, buyer counters, seller counters again, buyer accepts. Assert 4 revisions; last_modified_by toggles correctly; ACCEPT allowed only by the non-last-mover side (buyer, after seller's counter).
4. **`wf_otc_options_reject_flow_test.go`** — Offer created, then rejected by counterparty. Assert status REJECTED; no saga runs; buyer-balance and seller-holdings unchanged; `otc.offer-rejected` emitted.
5. **`wf_otc_options_insufficient_shares_test.go`** — Seller has 10 shares; writes two offers of 7 shares each. Second create-offer returns 409 `business_rule_violation` with message about available shares.
6. **`wf_otc_options_exercise_compensation_test.go`** — Configure a fault-injection hook (existing helper in `test-app/workflows/helpers_test.go` or to be added) making step 5 of the exercise saga fail. Assert buyer's debit is reversed, seller's credit is reversed, reservation is NOT released (contract stays ACTIVE so buyer can retry), and a `contract-failed` event is NOT emitted (the saga compensated cleanly). Buyer may retry exercise successfully.
7. **`wf_otc_options_crosscurrency_test.go`** — Seller's RSD account vs. buyer's EUR account. Exchange-service mock rate EUR/RSD = 117.5. Assert premium amounts in both currencies are equal after conversion; exercise amounts likewise; ledger entries on each side are in the correct account currency; `ccy_rate_used` returned in response matches the exchange-service response.
8. **`wf_otc_options_concurrent_accept_test.go`** — Two buyers call `/accept` on the same open offer simultaneously (goroutines in the test). Exactly one succeeds (201, contract created); the other gets 409. Seller's reserved shares equal only the winning offer's quantity.
9. **(stretch) `wf_otc_options_read_receipts_test.go`** — user A creates offer; user B lists, sees `unread:true`; B fetches detail; B lists again, `unread:false`; A counters; B lists, `unread:true` again.

**Coverage validation:** each test asserts **spec behavior** — ledger row count with matching memos, exact share-quantity arithmetic on holdings, kafka event payload, offer/contract status transitions — not just HTTP status codes (per CLAUDE.md testing requirement).

### 10.3 Helpers to add

- `test-app/workflows/helpers_test.go`:
  - `runOtcExpiryCron(t, ctx)` — invokes stock-service's cron trigger RPC (or HTTP debug endpoint behind a test-only flag).
  - `advanceTestClock(t, delta)` — moves the cron's logical date forward for time-based tests.
  - `injectSagaFault(t, sagaName, stepIndex, errorKind)` — fault-injection helper (may piggyback on existing `saga_log` seed pattern from `test-app/workflows/wf_saga_test.go` if it exists, else add it).
- `contract/testutil/`:
  - `NewOtcOfferFixture(...)` — builds a PENDING offer row + matching holding setup.
  - `AssertLedgerEntries(t, accountID, expected)` — shared assertion, may already exist; if not, factor out from §wf_purchase_test.

### 10.4 Running

- `make test` for unit tests.
- `make docker-up && go test ./test-app/workflows/ -run OTCOptions -v` for integration.
- `make lint` per CLAUDE.md.

---

## 11. Spec / REST doc updates

Per CLAUDE.md: `Specification.md` and `docs/api/REST_API_v1.md` must both be updated in the implementation PR (not this design spec — but the implementation plan must include the update steps).

### 11.1 `Specification.md` updates

- **§17 REST API** — add subsection "OTC Option Trading" listing the 9 routes from §7.
- **§18 Entities** — append:
  - `OTCOffer` (fields, indexes).
  - `OTCOfferRevision`.
  - `OptionContract`.
  - `OTCOfferReadReceipt`.
  - Extend the `HoldingReservation` entity description with the new `otc_contract_id` column and CHECK constraint.
- **§19 Kafka topics** — add the 8 `otc.*` topics from §8 with payload field lists.
- **§20 Enums** — add:
  - `otc_offer_status`: `PENDING`,`COUNTERED`,`ACCEPTED`,`REJECTED`,`EXPIRED`,`FAILED`.
  - `otc_offer_direction`: `sell_initiated`,`buy_initiated`.
  - `option_contract_status`: `ACTIVE`,`EXERCISED`,`EXPIRED`,`FAILED`.
  - Extend `system_type` coverage in OTC context.
- **§21 Business rules** — new rules:
  - Seller concurrent-contract invariant (§4.6).
  - Last-mover rule (§5.1).
  - Premium non-refundable on expiry.
  - Buyer-only exercise (intra-bank).
  - Idempotency-key scheme for OTC sagas.
- **§11 gRPC services** — add `stock.OTCOptionsService` with RPCs matching the 9 REST routes.
- **§6 Permissions** — add `otc.trade`.
- **§3 Gateway client wiring** — add `otcOptionsClient` to the wiring diagram.

### 11.2 `docs/api/REST_API_v1.md` updates

Add a new top-level section **"OTC Option Trading"** with one subsection per route per §7, following the existing format (auth, params, body, example request, all response codes including the 202/503 saga-in-progress cases for accept and exercise). Mark the legacy direct-purchase OTC section as "Deprecated — see OTC Option Trading".

### 11.3 Swagger

Every handler added in §7 gets full godoc annotations (`@Summary`, `@Tags OTCOptions`, `@Param`, `@Success`, `@Failure`, `@Router`). Regenerate via `make swagger`. The generated files in `api-gateway/docs/` are committed alongside handler changes.

### 11.4 docker-compose

- No new services or databases — stock-service already exists; it gains new tables via `AutoMigrate`.
- No new env vars except optional `OTC_EXPIRY_CRON_UTC` (default `02:00`) and `OTC_EXPIRY_BATCH_SIZE` (default `500`). Add both to `docker-compose.yml` **and** `docker-compose-remote.yml` stock-service environment blocks.
- No new `depends_on` entries needed (stock-service already depends on account-service, exchange-service, kafka, postgres).

---

## 12. Out of scope

Explicitly **not** in this spec (and where they live instead):

1. **Cross-bank OTC** (Spec 4, `2026-04-25-crossbank-otc-options-design.md`) — federated offer discovery, cross-bank premium settlement, cross-bank exercise saga via `BankRouter`. The `*_bank_code` fields and `external_correlation_id` are populated there.
2. **Funds-as-OTC-participants** — allowing a supervisor to act on behalf of a fund as buyer/seller. Requires `otc.exercise-on-behalf` permission and changes to identity resolution in gateway. Planned for Spec 3 (fund management).
3. **Option chains / standardized strikes / put options** — this spec covers only single-leg **call options** on stocks. Puts, spreads, strangles, etc., are future work.
4. **Secondary market (selling an already-accepted contract)** — no transfer of contract ownership. A contract is held by the original buyer until exercise or expiry.
5. **Early-exercise restrictions per underlying** — we treat all stocks as uniformly American-style. If a future regulatory requirement needs European-style (exercise only on settlement_date), add a `style` column and gate step 1 of the exercise saga on `today == settlement_date`.
6. **Automatic exercise in-the-money at expiry** — option either exercises or expires; no auto-assign. Explicit user action required to exercise.
7. **Margin / uncovered calls** — the seller must own the underlying shares (covered calls only). Naked shorting is not supported.
8. **Commission/fees on OTC trades** — premium flows buyer→seller and strike flows buyer→seller net, with no bank commission layer. (Spec 3 may add fee rules via the existing `transfer_fees` mechanism; this spec does **not**.)
9. **Notification UX details** — topic payloads are fixed here; wire-up to email/push templates is the notification-service's concern per existing patterns.
10. **Migration tooling for legacy OTC** — the legacy `otc_service.go` routes stay live. Removal is coordinated with Spec 4.

---

## 13. Open questions

1. **Should a supervisor ever exercise on behalf of a fund?**
   Default in this spec: **buyer-only** — caller's JWT must match the contract's `buyer_user_id` + `buyer_system_type`. If the buyer is a fund (owned by the bank), the supervisor acting for that fund authenticates as themselves and the fund's identity is the buyer; the supervisor's `otc.trade` + an as-yet-unspecified `fund.manage` permission gate the call. Deferred to Spec 3 for the fund-management plumbing.

2. **Max revision count on counters?**
   Default: **no cap**. Celina 4 describes the process as "back and forth" without limit. If UX observes runaway negotiations in practice, add a `MAX_OTC_REVISIONS` env var (default: unlimited → 50). Purely additive, no schema change.

3. **Premium refund on contract expiry?**
   **No.** Per Celina 4 the premium compensates the seller for reserving shares. On expiry the seller keeps the premium and regains share availability. Confirmed; no refund logic in this spec.

4. **Concurrent-accept races on an open offer.**
   Handled via the invariant in §4.6 (SELECT FOR UPDATE on seller's holding during step 2 of accept). Last writer loses with 409 `business_rule_violation`. See integration test §10.2 #8.

5. **What happens if the seller's account is closed/deactivated between accept and exercise?**
   Not explicitly handled here. The exercise saga will hit a `FailedPrecondition` from account-service's `CreditAccount`. Compensation reverses buyer's debit; contract stays ACTIVE; buyer can retry until settlement_date. After expiry, funds stay with buyer and seller keeps only the premium. Possibly worth a follow-up spec: "block closing an account that is referenced by an active option contract".

6. **Decimal precision and rounding on cross-currency exercise.**
   We store amounts as `DECIMAL(20,8)`. Exchange-service's `Convert` returns a fully-resolved decimal; we pass the returned value verbatim into `ReserveFunds`. No client-side rounding. This matches how cross-currency transfers already work.

7. **Should a client be able to see others' open offers (marketplace view)?**
   Celina 4 hints at a public OTC listing ("akcije koje su drugi klijenti prebacili na 'javni režim'"). This spec's `GET /api/v1/me/otc/offers` only returns the caller's offers. A marketplace endpoint `GET /api/v1/otc/offers?open=true` (listing offers with `counterparty_user_id IS NULL`) is useful but **deferred** to Spec 3 or a UX-follow-up.

8. **Holding-reservation cleanup after `FAILED` contract.**
   As designed, a FAILED contract's reservation is left intact pending ops review. Should we auto-release after a TTL? Likely yes, but a manual process is safer for v1. Operational runbook entry noted for the implementation PR.

9. **Employee-as-buyer/seller — is the employee trading on their own behalf or the bank's?**
   In this spec, `system_type='employee'` means the employee's personal account. Bank-proprietary OTC trading (employee acting as "bank desk") is a supervisor-fund feature and routed through Spec 3.

10. **Should `last_modified_by` on a COUNTERED offer also record the content of the prior revision inline (for easy UI diffing)?**
    Not needed — `otc_offer_revisions` captures every prior state, and `GET /otc/offers/{id}` returns the revision list. Client does the diff.

---

*End of spec. Estimated length ~1 000 lines. Review targets: seller-invariant enforcement at accept time, idempotency-key consistency across sagas, and the cross-bank seams (§3.7, §4 nullable `*_bank_code` columns) that Spec 4 will plug into.*
