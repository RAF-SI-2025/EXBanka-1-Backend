# Inter-Bank 2-Phase Commit Transfers — Design Spec

## 1. Status / Date / Supersedes

- **Status:** Design — ready for implementation-plan drafting.
- **Date:** 2026-04-24.
- **Branch:** `feature/securities-bank-safety`.
- **Spec position:** Spec 3 of 4 in the Celina 5 series.
  - Spec 1: securities bank safety (intra-bank).
  - Spec 2: ownership lockdown & loan disbursement.
  - **Spec 3 (this document): inter-bank 2-phase-commit transfers.**
  - Spec 4 (planned): cross-bank OTC securities settlement — will build on the transport layer defined here.
- **Supersedes:** none. This is the first formal design for inter-bank communication in the EXBanka backend. Future specs may amend the state machine or message envelope; those specs MUST explicitly list this document in their `Supersedes` section.

## 2. Motivation

### 2.1 Celina 5 requirement

Chapter "Komunikacija između banaka" of `docs/Celina 5(Nova).docx.md` (generation 2024/25) specifies that:

1. The four student-project banks must be able to move money across bank boundaries.
2. The receiving bank's identity is derived from the first three digits of the destination account number (`111xxx` — this bank, `222xxx`, `333xxx`, `444xxx` — peers).
3. The payment must execute atomically: either fully committed on both sides or fully rolled back. Partial state is not acceptable.
4. The protocol is a 2-Phase Commit:
   - Bank A initiates a **Prepare** request to Bank B with the originating amount and currency.
   - Bank B responds with **Ready** (FX rate + fees computed, destination amount confirmed) or **NotReady** (with a reason).
   - On Ready, Bank A sends a **Commit** and debits the sender; Bank B credits the receiver and confirms.
   - On NotReady or any failure, both banks release any reserved funds and the transaction ends cleanly.
5. The FX rate and fees are computed by the **receiving** bank at Prepare time. That rate is locked for Commit. The sending bank does not recompute.
6. A failed or timed-out transaction must be reconcilable. Banks must detect lost messages and resume/abort based on the peer's view of state.

### 2.2 SI-TX-PROTO 2024/25 reference

The canonical wire protocol is defined at https://arsen.srht.site/si-tx-proto/ (generation 2024/25). That URL is out of reach from this sandboxed design session.

This spec therefore defines the **shape and semantics** of the protocol in sufficient detail to be SI-TX-PROTO–compliant, but explicitly defers **exact field names, JSON casing, and wire-level enum strings** to the implementation plan. The implementation plan WILL fetch the SI-TX-PROTO page before writing code. Where this spec uses placeholder field names (e.g., `transactionId`, `finalAmount`), those placeholders MUST be reconciled with SI-TX-PROTO at implementation time.

### 2.3 Why not a new microservice

The transaction-service already owns:

- The transfer domain (intra-bank transfers via `POST /api/v1/transfers`).
- The saga log (`saga_log` table) and saga helper used for compensation of multi-step money movements.
- A `ReserveFunds` gRPC and its inverse `ReleaseFunds`, already consumed for stock and loan flows.
- Kafka publication of transfer lifecycle events.

Inter-bank transfer is an extension of the transfer domain, not a new domain. Adding a new microservice would require duplicating saga machinery, FX lookup, fee calculation, and ledger coordination. Extending transaction-service reuses all of that. The api-gateway exposes a small number of **internal** HTTP endpoints (HMAC-authenticated, not JWT) that forward into transaction-service over the existing gRPC client.

## 3. Architecture Overview

### 3.1 Component map

```
                                    ┌─────────────────────────────┐
        client POST /api/v1/        │        api-gateway          │
        transfers (JWT)             │                             │
         ─────────────────────────▶ │  AnyAuthMiddleware          │
                                    │  TransferHandler            │
                                    │    ├─ intra-bank path       │
                                    │    └─ inter-bank path       │
                                    │                             │
        peer bank POST /internal/   │  InterBankHandler           │
        inter-bank/... (HMAC)       │  HMACMiddleware             │
         ─────────────────────────▶ │    ├─ prepare               │
                                    │    ├─ commit                │
                                    │    └─ check-status          │
                                    └──────────────┬──────────────┘
                                                   │  gRPC
                                                   ▼
                                    ┌─────────────────────────────┐
                                    │     transaction-service     │
                                    │                             │
                                    │  InterBankService           │
                                    │    ├─ InitiateOutgoing()    │
                                    │    ├─ HandlePrepare()       │
                                    │    ├─ HandleCommit()        │
                                    │    ├─ HandleCheckStatus()   │
                                    │    └─ Reconciler (cron)     │
                                    │                             │
                                    │  PeerBankClient (HTTP)      │
                                    │    ├─ SendPrepare()         │
                                    │    ├─ SendCommit()          │
                                    │    └─ SendCheckStatus()     │
                                    │                             │
                                    │  Repositories:              │
                                    │    ├─ BanksRepository       │
                                    │    └─ InterBankTxRepo       │
                                    │                             │
                                    │  Reuses:                    │
                                    │    ├─ SagaLog (existing)    │
                                    │    ├─ Ledger via account-   │
                                    │    │   service gRPC         │
                                    │    └─ ExchangeService RPC   │
                                    └──────────────┬──────────────┘
                                                   │
                              ┌────────────────────┴─────────────────────┐
                              │                                          │
                              ▼                                          ▼
                    ┌─────────────────┐                         ┌─────────────────┐
                    │ account-service │                         │  Kafka topics   │
                    │  ReserveFunds / │                         │  transfer.      │
                    │  ReleaseFunds / │                         │  interbank-*    │
                    │  CommitReserved │                         └─────────────────┘
                    └─────────────────┘
```

### 3.2 Data flow — outgoing (this bank = sender)

1. Client calls `POST /api/v1/transfers` with a receiver account whose prefix is not `111` (this bank's code).
2. Gateway detects the inter-bank case from the first 3 digits of the receiver account, and calls the transaction-service's new `InitiateInterBankTransfer` gRPC.
3. Transaction-service inserts a row into `inter_bank_transactions` with status `initiated`, calls `ReserveFunds` on account-service to freeze the sender's money, and transitions the row to `preparing`.
4. Transaction-service synchronously (with a 30-second timeout) sends a **Prepare** request to the peer bank's base URL over HTTPS.
5. On receiving a **Ready** response, the row moves to `ready_received`. Transaction-service then sends a **Commit** and, on HTTP 200, runs the local debit inside a saga step: it calls account-service's `CommitReservedFunds` which converts the reserved balance into an actual debit with a ledger entry. Status becomes `committed`. A `transfer.interbank-committed` Kafka event is published after DB commit.
6. On receiving **NotReady**, transaction-service calls `ReleaseFunds` on account-service, transitions the row to `rolled_back`, publishes `transfer.interbank-rolled-back`, and returns the reason to the gateway.
7. If the peer is unreachable or the timeout fires, the row moves to `reconciling`. A background cron every 60 seconds calls the peer's `/internal/inter-bank/check-status` endpoint. The response determines the next transition.

### 3.3 Data flow — incoming (this bank = receiver)

1. Peer bank POSTs to this bank's `POST /internal/inter-bank/transfer/prepare` with HMAC header.
2. Gateway's HMACMiddleware verifies the `X-Bank-Code` exists in `banks` and the `X-Bank-Signature` matches `HMAC-SHA256(api_key, body)`.
3. Gateway forwards to transaction-service's `HandlePrepare` gRPC.
4. Transaction-service inserts an `inter_bank_transactions` row with `role=receiver`, status `prepare_received`. It then:
   - Looks up the receiver account; if missing or inactive → transition to `final_notready`, respond **NotReady**.
   - Looks up the receiver's currency; if it differs from the incoming currency, calls exchange-service's `Convert` RPC to get an FX rate.
   - Applies any inbound transfer fee from the existing `transfer_fees` table (fees apply to the converted amount, in the receiver's currency).
   - Calls `ReserveFunds` on the **receiver** account — this is a *credit reservation* variant (see section 4.3): a hold on incoming funds so a subsequent Commit is guaranteed to succeed. Status transitions to `validated` → `ready_sent` after the HTTP response is queued.
5. Peer sends `POST /internal/inter-bank/transfer/commit`. Gateway verifies HMAC. Transaction-service transitions `ready_sent` → `commit_received`, calls account-service to finalize the credit (turn the credit reservation into an actual credit + ledger entry), moves status to `committed`, publishes `transfer.interbank-received`.
6. If the Commit never arrives (peer crash, etc.), a timeout cron (runs every minute, looks at `ready_sent` rows older than 2× the prepare timeout) releases the credit reservation and marks the row `abandoned`.

### 3.4 What stays untouched

- Intra-bank transfers continue to use the existing handler path unchanged. The gateway's decision (prefix `111` vs other) is the only new branch.
- `transfers` table is unaffected; the inter-bank domain has its own `inter_bank_transactions` table.
- `saga_log` entries continue to be written for local multi-step DB work. The inter-bank state machine is NOT stored in `saga_log`; it has its own richer state columns. (The two coexist — a single outgoing interbank transfer may produce one `inter_bank_transactions` row and a handful of `saga_log` entries for local debits/credits.)
- Existing Kafka topics are unchanged; new topics are additive.
- Intra-bank transfer fees (`transfer_fees` table) apply unchanged. Inter-bank fees on incoming funds reuse the same table; outgoing inter-bank fees are defined per-row in `inter_bank_transactions.fees_rsd` and are **not** charged by this bank when it's the sender — the receiving bank's fees apply.

## 4. Data Model

### 4.1 `banks` — peer bank registry

Stored in transaction-service's database (`transaction_db`, port 5437). Seeded on startup with this bank's own row and any known peers.

| Column              | Type         | Notes                                                                                        |
|---------------------|--------------|----------------------------------------------------------------------------------------------|
| `code`              | varchar(3)   | Primary key. First three digits of an account number. `111` is this bank.                    |
| `name`              | varchar(128) | Human-readable name, e.g. "Bank B".                                                         |
| `base_url`          | varchar(256) | Full URL, e.g. `https://bank-b.example/internal/inter-bank`.                                 |
| `api_key_bcrypted`  | varchar(60)  | Bcrypt hash of the shared API key this bank uses to sign outbound requests to that peer.      |
| `inbound_api_key_bcrypted` | varchar(60) | Bcrypt hash of the key the peer uses to sign requests inbound to us. May equal `api_key_bcrypted` per peer agreement, but modeled separately. |
| `public_key`        | text NULL    | Reserved for a future migration to signed envelopes. Unused in v1.                           |
| `active`            | boolean      | When false, inbound and outbound calls with this peer are rejected (503).                   |
| `created_at`        | timestamptz  |                                                                                              |
| `updated_at`        | timestamptz  |                                                                                              |

Seed entries:

- `("111", "EXBanka", local-base-url, this_bank_self_hmac, this_bank_self_hmac, null, true, ...)` — optional; useful for local round-trip testing in test-app.
- `("222", "Bank B", <from env>, <from env>, <from env>, null, true, ...)` — template seed; actual values come from env vars `PEER_222_BASE_URL`, `PEER_222_OUTBOUND_KEY`, `PEER_222_INBOUND_KEY`. Similar for `333` and `444`.

Admin endpoint to change these is **out of scope** (see section 14). Updates in v1 happen via direct DB inserts in the seed migration or by env re-deploy.

### 4.2 `inter_bank_transactions` — durable 2PC state

| Column                     | Type          | Notes                                                                                      |
|----------------------------|---------------|--------------------------------------------------------------------------------------------|
| `tx_id`                    | uuid          | Primary key. Generated by the sender; for `role=receiver`, copied from the Prepare message. |
| `role`                     | enum          | `sender` or `receiver`.                                                                    |
| `remote_bank_code`         | varchar(3)    | FK to `banks.code`. The other side of this transfer.                                       |
| `sender_account_number`    | varchar(20)   |                                                                                            |
| `receiver_account_number`  | varchar(20)   |                                                                                            |
| `amount_native`            | numeric(20,4) | The original amount as the sender specified it.                                            |
| `currency_native`          | varchar(3)    | Sender's account currency.                                                                 |
| `amount_final`             | numeric(20,4) NULL | Set when Ready is issued (receiver) or received (sender).                              |
| `currency_final`           | varchar(3) NULL | Receiver's account currency.                                                              |
| `fx_rate`                  | numeric(20,8) NULL | Locked at Ready.                                                                        |
| `fees_rsd`                 | numeric(20,4) NULL | Fees in the receiver's currency (misleading column name kept generic — see note).       |
| `phase`                    | enum          | `prepare`, `commit`, `reconcile`, `done`.                                                  |
| `status`                   | enum          | See section 5.3 for the full enum list.                                                    |
| `error_reason`             | text NULL     | Set on any `notready_*`, `rolled_back`, `abandoned`, `final_notready` status.             |
| `retry_count`              | integer       | Incremented each time the reconciler re-sends check-status.                                |
| `payload_json`             | jsonb         | The most recent inbound or outbound message body, for debugging and replay.                |
| `idempotency_key`          | varchar(64)   | Unique. Senders use `sha256(tx_id + role)`; receivers use the received `tx_id`.            |
| `created_at`               | timestamptz   |                                                                                            |
| `updated_at`               | timestamptz   |                                                                                            |
| `version`                  | bigint        | For optimistic locking — see CLAUDE.md concurrency requirement.                            |

**Indices:**
- `UNIQUE (tx_id, role)` — senders and receivers can both hold a row for the same tx_id in local-loopback testing; otherwise one row per (tx_id, role).
- `UNIQUE (idempotency_key)`.
- `INDEX (status)` — hot for the reconciler.
- `INDEX (updated_at)` — hot for the timeout cron.
- `INDEX (remote_bank_code)`.

**Note on `fees_rsd`:** the column name is legacy from the earlier intra-bank design; for inter-bank we store fees in the **currency_final** currency. The implementation plan SHOULD rename it to `fees_final` before the first migration runs.

### 4.3 Account-service additions

One new gRPC method is added to account-service to support inbound credit reservation:

- `ReserveIncoming(account_id, amount, currency, reservation_id) → (balance_after, reservation_id)` — creates a pending credit reservation row without actually moving the balance. The credit is "promised"; the subsequent `CommitIncoming` finalizes it.
- `CommitIncoming(reservation_id) → (balance_after)` — turns the reservation into an actual credit + ledger entry.
- `ReleaseIncoming(reservation_id)` — cancels the reservation with no ledger impact.

These three RPCs are symmetric to the existing `ReserveFunds` / `CommitReserved` / `ReleaseFunds` trio, but for credits rather than debits.

Rationale: a Prepare on the receiver side needs to prove the account can accept the funds (exists, active, currency supported) **and** bind the destination so that a subsequent Commit cannot be denied. Reservation is the cleanest way to hold that promise durably.

Alternatives considered:
- "Just credit immediately on Commit": rejected because the Commit handler must be idempotent and must not double-credit. A reservation gives us a unique key to dedupe against.
- "Use the sender-side ReserveFunds with a negative amount": rejected as too clever; negative reservations break every assumption in existing code.

## 5. State Machines

### 5.1 Sender state machine

```
             ┌──────────────┐
             │   INITIATED  │  row inserted, funds not yet reserved
             └──────┬───────┘
                    │ ReserveFunds() success
                    ▼
             ┌──────────────┐
             │   PREPARING  │  Prepare sent, awaiting Ready/NotReady
             └──────┬───────┘
                    │
        ┌───────────┼───────────┬────────────────┐
        │ Ready     │ NotReady  │ timeout        │ HTTP 5xx
        ▼           ▼           ▼                ▼
┌──────────────┐ ┌────────────────┐ ┌──────────────┐ ┌──────────────┐
│READY_RECEIVED│ │NOTREADY_RECVD  │ │ RECONCILING  │ │ RECONCILING  │
└──────┬───────┘ └──────┬─────────┘ └──────┬───────┘ └──────┬───────┘
       │ Send Commit    │ ReleaseFunds()    │ cron: check-status
       ▼                ▼                   │
┌──────────────┐ ┌──────────────┐           │
│  COMMITTING  │ │ ROLLED_BACK  │           │
└──────┬───────┘ └──────────────┘           │
       │ CommitReserved() success           │
       │ + HTTP 200 from peer               │
       ▼                                    │
┌──────────────┐                            │
│  COMMITTED   │◀───────────────────────────┘ (peer reported committed)
└──────────────┘                            │
                                            │
                   ┌────────────────────────┘ (peer reported rolled_back
                   │                          or unknown past threshold)
                   ▼
             ┌──────────────┐
             │ ROLLED_BACK  │ ← ReleaseFunds() called defensively
             └──────────────┘
```

### 5.2 Receiver state machine

```
             ┌────────────────────┐
             │ PREPARE_RECEIVED   │  row inserted from inbound Prepare
             └──────┬─────────────┘
                    │ validate account + currency + limits
         ┌──────────┼──────────┐
         │ all ok   │ any fail
         ▼          ▼
┌────────────┐ ┌──────────────────┐
│ VALIDATED  │ │  NOTREADY_SENT   │
└──────┬─────┘ └──────┬───────────┘
       │              │ (HTTP 200 with NotReady body)
       │ ReserveIncoming() success
       ▼              ▼
┌──────────────┐ ┌─────────────────┐
│ READY_SENT   │ │ FINAL_NOTREADY  │  terminal
└──────┬───────┘ └─────────────────┘
       │
   ┌───┴────────────────┐
   │ Commit arrives     │ timeout awaiting Commit
   ▼                    ▼
┌────────────────┐ ┌──────────────┐
│COMMIT_RECEIVED │ │  ABANDONED   │
└──────┬─────────┘ └──────┬───────┘
       │                  │ ReleaseIncoming()
       │ CommitIncoming() │
       ▼                  ▼ terminal
┌──────────────┐
│  COMMITTED   │  terminal
└──────────────┘
```

### 5.3 Enum of all `status` values

```
initiated           sender row inserted, no external calls yet
preparing           sender, Prepare sent, awaiting response
ready_received      sender, peer answered Ready; Commit about to be sent
notready_received   sender, peer answered NotReady; funds released
committing          sender, Commit sent, awaiting peer 200 + local CommitReserved
committed           sender OR receiver, terminal success
rolled_back         sender, terminal failure (NotReady OR reconciliation-rollback)
reconciling         sender, peer unreachable or timeout; cron probing
abandoned           receiver, Commit never arrived; ReserveIncoming released
prepare_received    receiver row inserted
validated           receiver, checks passed, reservation about to happen
ready_sent          receiver, Ready response queued; awaiting Commit
notready_sent       receiver, NotReady response queued (briefly, before final_notready)
commit_received     receiver, Commit arrived; CommitIncoming in progress
final_notready      receiver, terminal NotReady
```

Note: `notready_sent` and `final_notready` are distinct because `notready_sent` is a transient state while the HTTP response is still being written; it transitions to `final_notready` atomically after response flush.

### 5.4 Illegal transitions and how they're prevented

- Optimistic locking (`version` column) prevents two concurrent updates from racing — the second one fails and the caller retries.
- The repo layer exposes a single `UpdateStatus(tx_id, expected_from, expected_to)` method that enforces the transition matrix; unknown transitions return `ErrIllegalTransition`.
- A transition matrix is encoded in `internal/interbank/transitions.go` (implementation file, design placeholder). For each `from → to` pair, it records whether the transition is legal and what side-effects are required (e.g., `READY_RECEIVED → COMMITTING` MUST call `CommitReserved`).

## 6. Message Shapes

All message shapes below are **design placeholders**. The implementation plan MUST cross-reference SI-TX-PROTO 2024/25 (https://arsen.srht.site/si-tx-proto/) and rename fields to match. If SI-TX-PROTO uses, say, `txId` instead of `transactionId` or `finalValue` instead of `finalAmount`, the implementation code MUST conform — this spec does not override the wire protocol.

The **shape** (which fields, what meaning, which states they apply to) is locked by this spec; only field **names** are deferred.

### 6.1 Envelope

Every message has a consistent envelope:

```json
{
  "transactionId": "f6d4f2a1-9c3e-4f2b-8c3a-1d1e1f1a1b1c",
  "action": "Prepare | Ready | NotReady | Commit | Committed | CheckStatus | Status",
  "senderBankCode": "111",
  "receiverBankCode": "222",
  "timestamp": "2026-04-24T14:15:22Z",
  "body": { /* action-specific */ }
}
```

The `action` value determines the body schema. For Spec 4 (cross-bank OTC), additional `action` values will be added: `ReserveShares`, `ReserveSharesConfirm`, `ReserveSharesFail`, `CommitFunds`, `TransferOwnership`. This spec reserves those names and requires that the receiver's action dispatcher be extensible — a single `switch action` in one place.

### 6.2 Prepare (sender → receiver)

```json
{
  "action": "Prepare",
  "transactionId": "uuid",
  "body": {
    "senderAccount": "1110000000001234",
    "receiverAccount": "2220000000005678",
    "amount": "1000.00",
    "currency": "RSD",
    "memo": "payment for invoice 42"
  }
}
```

### 6.3 Ready (receiver → sender)

```json
{
  "action": "Ready",
  "transactionId": "uuid",
  "body": {
    "status": "Ready",
    "originalAmount": "1000.00",
    "originalCurrency": "RSD",
    "finalAmount": "8.50",
    "finalCurrency": "EUR",
    "fxRate": "117.65",
    "fees": "0.00",
    "validUntil": "2026-04-24T14:15:52Z"
  }
}
```

`validUntil` is informational — the sender's Commit must arrive before this timestamp; after it, the receiver is free to abandon the row.

### 6.4 NotReady (receiver → sender)

```json
{
  "action": "NotReady",
  "transactionId": "uuid",
  "body": {
    "status": "NotReady",
    "reason": "insufficient_funds | account_not_found | account_inactive | currency_not_supported | limit_exceeded | bank_inactive | unknown"
  }
}
```

### 6.5 Commit (sender → receiver)

```json
{
  "action": "Commit",
  "transactionId": "uuid",
  "body": {
    "finalAmount": "8.50",
    "finalCurrency": "EUR",
    "fxRate": "117.65",
    "fees": "0.00"
  }
}
```

Fields mirror the Ready response so the receiver can verify the sender is committing to the same deal it proposed. Any mismatch → receiver returns HTTP 409 with `reason: "commit_mismatch"` and the sender transitions to `reconciling`.

### 6.6 Committed (receiver → sender, HTTP 200 body of Commit)

```json
{
  "action": "Committed",
  "transactionId": "uuid",
  "body": {
    "creditedAt": "2026-04-24T14:15:24Z",
    "creditedAmount": "8.50",
    "creditedCurrency": "EUR"
  }
}
```

### 6.7 CheckStatus (either direction)

```json
{
  "action": "CheckStatus",
  "transactionId": "uuid",
  "body": {}
}
```

Response:

```json
{
  "action": "Status",
  "transactionId": "uuid",
  "body": {
    "role": "sender | receiver",
    "status": "<any value from section 5.3>",
    "finalAmount": "…",
    "finalCurrency": "…",
    "updatedAt": "2026-04-24T14:15:24Z"
  }
}
```

### 6.8 Versioning

The envelope includes an optional `protocolVersion` field (e.g., `"2024/25"`); if present and mismatched, the receiver responds with HTTP 426 Upgrade Required. Absent `protocolVersion` is treated as 2024/25 for backward compatibility in v1.

## 7. REST API Surface

### 7.1 Public routes (api-gateway, JWT authenticated)

#### 7.1.1 `POST /api/v1/transfers` — extended

Existing body shape is preserved. The gateway inspects the first three characters of `receiverAccount`:

- If they equal this bank's code (`111`): forward to existing intra-bank transfer handler (unchanged).
- If they are a known code in `banks` with `active=true`: forward to the new inter-bank handler.
- If they are an unknown code or an inactive bank: return **400 validation_error** with `code="unknown_bank"`.

**New response semantics:**

For inter-bank, the gateway returns `202 Accepted` immediately after the Prepare has been sent (or immediately after the saga commits the `initiated → preparing` transition; see implementation plan for the exact sync point):

```json
{
  "transactionId": "f6d4f2a1-...",
  "status": "preparing",
  "pollUrl": "/api/v1/me/transfers/f6d4f2a1-..."
}
```

Intra-bank responses are unchanged (`201 Created` with the final transfer record).

Required input validation (api-gateway):
- `receiverAccount` — 16 digits, first 3 digits MUST match an entry in `banks`.
- `amount` — positive decimal, at most 4 fractional digits.
- `currency` — ISO 4217 3-letter code in the accepted set.
- `memo` — optional, max 140 chars.

#### 7.1.2 `GET /api/v1/me/transfers/{id}` — extended

Already exists for intra-bank transfers. Extended to also look up `inter_bank_transactions` if no row is found in `transfers`. Response is a unified shape:

```json
{
  "transactionId": "…",
  "kind": "intra_bank | inter_bank_out | inter_bank_in",
  "status": "preparing | ready_received | committing | completed | rejected | …",
  "amount": "…",
  "currency": "…",
  "finalAmount": "…",
  "finalCurrency": "…",
  "fxRate": "…",
  "fees": "…",
  "errorReason": "…",
  "createdAt": "…",
  "updatedAt": "…"
}
```

The user-facing `status` values are a simplified mapping from the internal enum:
- `preparing` ← `initiated | preparing`
- `ready_received` ← `ready_received | committing`
- `completed` ← `committed`
- `rejected` ← `notready_received | rolled_back | abandoned | final_notready`
- `reconciling` ← `reconciling` (users can see this so support can explain delays)

#### 7.1.3 `GET /api/v1/me/transfers?status=<...>` — extended

Listing endpoint gains the `status` filter. Allowed values: the user-facing enum from 7.1.2. Allows clients to poll all their in-flight inter-bank transfers.

### 7.2 Internal routes (api-gateway, HMAC authenticated)

These are served by api-gateway and forwarded to transaction-service over gRPC. They are NOT exposed through JWT middleware. They live under `/internal/inter-bank/...`.

#### 7.2.1 `POST /internal/inter-bank/transfer/prepare`

Request body: the Prepare envelope (section 6.2).
Headers: `X-Bank-Code`, `X-Bank-Signature`, `X-Idempotency-Key` (= `transactionId`).
Response 200: Ready (6.3) or NotReady (6.4).
Response 400: malformed envelope.
Response 401: HMAC failure.
Response 503: sender's bank row is `active=false` on our side.

#### 7.2.2 `POST /internal/inter-bank/transfer/commit`

Request body: the Commit envelope (section 6.5).
Headers: same as Prepare.
Response 200: Committed (6.6).
Response 404: unknown `transactionId` on our side — sender should move to reconciling and retry after cron wake.
Response 409: `commit_mismatch` — finals don't match our Ready.

#### 7.2.3 `POST /internal/inter-bank/check-status`

Request body: CheckStatus (6.7).
Headers: same as Prepare.
Response 200: Status response (6.7).
Response 404: we have no record of this transactionId. Caller should treat as "peer has no memory" and roll back locally after a grace period.

### 7.3 Swagger + REST doc updates

All public routes (7.1) are updated in `api-gateway/docs/` (swagger regeneration required) and in `docs/api/REST_API_v1.md`. Internal routes (7.2) are documented in a new section of `REST_API_v1.md` titled "Internal inter-bank endpoints (HMAC-authenticated)" with a prominent note that these routes are not for end users.

## 8. Authentication

### 8.1 HMAC model

Every internal inter-bank request carries:

- `X-Bank-Code: 222` — the sender's bank code, a 3-digit string. Receiver looks it up in `banks`.
- `X-Bank-Signature: <hex-encoded HMAC-SHA256>` — computed as `HMAC-SHA256(inbound_api_key_of_sender_from_receiver_perspective, canonical_body)`.
- `X-Idempotency-Key: <transactionId>` — echoes the envelope's transactionId for at-the-edge dedup in api-gateway before hitting transaction-service.
- `X-Timestamp: <RFC3339>` — the sender's clock.
- `X-Nonce: <random 16 bytes hex>` — single-use per request.

Canonical body = the raw request body bytes, no transformation.

### 8.2 Receiver verification steps

1. Parse headers; any missing → 400.
2. Look up `banks` row by `X-Bank-Code`. If missing or `active=false` → 401 (don't reveal which).
3. Verify `bcrypt.Compare(inbound_api_key_bcrypted, provided_key_from_header)` — but the provided key is NOT sent in the header. Instead the receiver stores the **plaintext** inbound api key in a secrets store (Docker env), bcrypts it at rest only for audit, and uses the plaintext at verification time. (**Correction for implementation plan:** the `_bcrypted` columns are for auditability and rotation. Runtime HMAC verification needs the plaintext key; store it in env as `PEER_<CODE>_INBOUND_KEY` and bootstrap the `banks` row with the bcrypt hash for compare-on-rotation.)
4. Recompute `HMAC-SHA256(plaintext_inbound_key, body)`. Constant-time compare with `X-Bank-Signature`. Mismatch → 401.
5. Verify `X-Timestamp` is within ±5 minutes of server time; outside → 401. (Mitigates replay.)
6. Verify `X-Nonce` is not in Redis `inter_bank_nonce:<code>:<nonce>`; if present → 401. Otherwise insert with 10-minute TTL. (Mitigates replay inside the timestamp window.)
7. Pass the request to transaction-service with a header attesting the HMAC was checked.

### 8.3 Sender signing steps

- Compute the canonical body as the marshaled JSON bytes it's about to POST.
- Sign with `HMAC-SHA256(outbound_api_key_for_this_peer, body)`.
- Set all five headers.
- POST over HTTPS. (Local Docker testing uses HTTP between containers with the same HMAC — TLS termination at the edge is out of scope for v1.)

### 8.4 Key rotation

v1: manual. Operator updates `banks.api_key_bcrypted` in DB and the corresponding env var, then restarts transaction-service. No rolling-rotation window in v1.
v2 (deferred): two active keys per peer with overlapping validity windows, so rotation is zero-downtime. Not in this spec.

## 9. Reconciliation + Timeout Policy

### 9.1 Timeout values

Configurable via `system_settings` table (existing in transaction-service); seeded defaults:

| Setting                                | Default |
|----------------------------------------|---------|
| `interbank.prepare_timeout_seconds`    | 30      |
| `interbank.commit_timeout_seconds`     | 30      |
| `interbank.receiver_commit_wait_seconds` | 90    |
| `interbank.reconcile_interval_seconds` | 60      |
| `interbank.reconcile_max_retries`      | 10      |
| `interbank.reconcile_stale_after_hours` | 24     |

### 9.2 Sender timeout on Prepare

- HTTP client timeout = `prepare_timeout_seconds`.
- On timeout or any network error: transition `preparing → reconciling`, increment `retry_count`, do NOT release funds yet (the peer might have received the Prepare and be about to commit).

### 9.3 Sender timeout on Commit

- HTTP client timeout = `commit_timeout_seconds`.
- On timeout: transition `committing → reconciling`. Funds remain reserved (they may have been debited on the peer side; we need to know before we can do anything).

### 9.4 Reconciler cron

Runs every `reconcile_interval_seconds`. Implementation pattern mirrors `stock-service`'s `SagaRecovery` cron.

For each row in status `reconciling`:

1. If `retry_count >= reconcile_max_retries` OR `updated_at < now() - reconcile_stale_after_hours`:
   - Transition to `rolled_back`, call `ReleaseFunds`, publish `transfer.interbank-rolled-back`. This is the "give up" path. An operator alert is emitted via Kafka (`ops.alert`, out of scope for this spec but documented here).
2. Otherwise call `POST /internal/inter-bank/check-status` against the peer.
3. Interpret the response:
   - Peer says `committed` → we also commit locally: debit the sender via `CommitReservedFunds`, transition to `committed`, publish `transfer.interbank-committed`.
   - Peer says `final_notready` or `abandoned` or `rolled_back` → release funds, transition to `rolled_back`, publish `transfer.interbank-rolled-back`.
   - Peer says `ready_sent` or `commit_received` → peer is still mid-flow. Increment `retry_count`, stay in `reconciling`.
   - Peer says 404 — unknown transactionId: if our `updated_at` is older than a grace window (2× `prepare_timeout_seconds`), release funds and roll back. Peer genuinely never saw us.
   - Peer 5xx or network error: increment `retry_count`, stay in `reconciling`.

### 9.5 Receiver timeout on Commit

A separate cron (also every `reconcile_interval_seconds`) scans receiver rows in status `ready_sent` with `updated_at < now() - receiver_commit_wait_seconds`. For each:

- Call `ReleaseIncoming` to unblock the destination account.
- Transition to `abandoned`.
- Publish `transfer.interbank-rolled-back`.
- If the sender later sends a Commit for this tx_id, we return 404 (unknown) and let them reconcile to rollback.

### 9.6 Crash recovery on startup

On transaction-service startup, a one-shot recovery routine:

1. Loads all rows in non-terminal status (`preparing`, `committing`, `reconciling`, `ready_sent`, `commit_received`).
2. For sender rows in `preparing` or `committing` older than `prepare_timeout_seconds`: transition to `reconciling`.
3. For receiver rows in `commit_received`: re-run `CommitIncoming` (it's idempotent on `reservation_id`) and transition to `committed`. This covers the "we credited the receiver in-memory but crashed before writing `committed`" case.
4. Leave the rest for the regular cron.

## 10. Kafka Topics

All topics are new and additive. Pre-created at transaction-service startup via `EnsureTopics`.

| Topic                              | Producer             | Consumer(s)                        | When published                                                            |
|------------------------------------|----------------------|------------------------------------|---------------------------------------------------------------------------|
| `transfer.interbank-prepared`      | transaction-service  | notification-service, audit        | Sender: after Ready received. Receiver: after Ready sent.                 |
| `transfer.interbank-committed`     | transaction-service  | notification-service, audit        | Sender: after local CommitReserved + peer 200.                            |
| `transfer.interbank-received`      | transaction-service  | notification-service, audit        | Receiver: after CommitIncoming.                                           |
| `transfer.interbank-rolled-back`   | transaction-service  | notification-service, audit        | Any side, on terminal failure / reconciled rollback / receiver abandon.   |

Message payload shape (shared across topics):

```json
{
  "transactionId": "…",
  "role": "sender | receiver",
  "remoteBankCode": "222",
  "status": "…",
  "amountNative": "…",
  "currencyNative": "…",
  "amountFinal": "…",
  "currencyFinal": "…",
  "fxRate": "…",
  "fees": "…",
  "errorReason": "…",
  "timestamp": "…"
}
```

All four topics MUST be in the `EnsureTopics` call of:
- transaction-service (producer + potential self-consumer for audit).
- notification-service (consumer — will send user-facing emails/push on commit/rollback).

Events are published **after the relevant DB transaction commits**, consistent with the concurrency requirements in CLAUDE.md.

## 11. Permissions

No new permissions.

- `transfers.create` (existing) is the permission required to POST `/api/v1/transfers`. It applies uniformly whether the transfer is intra-bank or inter-bank.
- `/api/v1/me/transfers/{id}` is authenticated via `AnyAuthMiddleware` as before (owner-based, no permission string needed).
- Internal `/internal/inter-bank/*` endpoints are NOT permission-gated. They are HMAC-gated only.

The `EmployeeSupervisor` role's existing transfer-approval permissions (for high-value transfers, if any exist in Spec 2's limits) apply before the inter-bank flow starts. Once the sender has entered the inter-bank flow, no additional authorization is required from within this bank — authorization of the destination side is the peer bank's responsibility.

Future specs may add `interbank.admin` for the bank-registry CRUD endpoint (out of scope here).

## 12. Testing Strategy

### 12.1 Unit tests

**transaction-service/internal/service** (inter-bank service):
- `TestInitiateOutgoing_Success` — happy path, ReserveFunds + Prepare + Ready + Commit + CommitReserved.
- `TestInitiateOutgoing_NotReady_ReleasesFunds` — peer says NotReady → ReleaseFunds called, status `rolled_back`.
- `TestInitiateOutgoing_PrepareTimeout_TransitionsToReconciling` — HTTP client times out → status `reconciling`, `retry_count=0`.
- `TestInitiateOutgoing_InsufficientFunds_NoOutbound` — ReserveFunds fails → no Prepare sent, status `rolled_back`.
- `TestHandlePrepare_Success_ReservesIncoming` — receiver path happy.
- `TestHandlePrepare_AccountNotFound_NotReady` — NotReady with reason `account_not_found`.
- `TestHandlePrepare_CurrencyConversion_AppliesFxAndFees`.
- `TestHandlePrepare_Idempotent_SameTxIdReturnsSameResponse` — replay of a Prepare returns the original decision without re-validating.
- `TestHandleCommit_Idempotent` — second Commit for the same tx_id returns the original Committed envelope.
- `TestHandleCommit_Mismatch_Returns409` — finals differ from the Ready we issued.
- `TestHandleCheckStatus_ReturnsCurrentState`.
- `TestReconciler_PeerCommitted_MovesUsToCommitted`.
- `TestReconciler_PeerRolledBack_MovesUsToRolledBack`.
- `TestReconciler_MaxRetries_GivesUpAndRollsBack`.
- `TestReceiverTimeoutCron_AbandonsStaleReadySent`.
- `TestRecoveryOnStartup_ResumesInFlight`.

**api-gateway/internal/middleware**:
- `TestHMACMiddleware_ValidSignature_Passes`.
- `TestHMACMiddleware_UnknownBankCode_Returns401`.
- `TestHMACMiddleware_InactiveBank_Returns401`.
- `TestHMACMiddleware_BadSignature_Returns401`.
- `TestHMACMiddleware_StaleTimestamp_Returns401`.
- `TestHMACMiddleware_NonceReplay_Returns401`.
- `TestHMACMiddleware_MissingHeaders_Returns400`.

**api-gateway/internal/handler/transfer**:
- `TestPostTransfer_IntraBank_RoutesToExistingHandler`.
- `TestPostTransfer_InterBank_Returns202WithTxId`.
- `TestPostTransfer_UnknownBankPrefix_Returns400`.
- `TestPostTransfer_InactivePeer_Returns400`.

### 12.2 Integration tests (test-app/workflows)

A **"mock bank B"** minimal HTTP server is introduced in `test-app/peerbank/` (new package). It:
- Listens on a test port.
- Implements the three `/internal/inter-bank/*` endpoints.
- Is configurable per test: Ready / NotReady / timeout / commit-mismatch / 5xx / silent-drop.
- Records every inbound request for assertions.
- Signs its outbound responses with a test HMAC key that matches the `banks` seed.

New workflows in `test-app/workflows/`:

- `wf_interbank_success_test.go` — client initiates inter-bank transfer → 202 + polling → eventual `completed` status; verify funds debited, peer "received" Commit, all four Kafka events fired in order.
- `wf_interbank_notready_test.go` — mock bank answers NotReady → client sees `rejected`, funds unreserved, `rolled_back` Kafka event.
- `wf_interbank_prepare_timeout_test.go` — mock bank delays response past timeout → status goes `reconciling`; mock bank is then instructed to answer `committed` on `check-status` → sender reconciles to `completed`.
- `wf_interbank_commit_timeout_test.go` — mock bank returns Ready, then hangs on Commit → sender goes to `reconciling`; `check-status` returns `ready_sent`; sender stays reconciling; mock eventually says `committed` → sender reconciles.
- `wf_interbank_commit_mismatch_test.go` — sender sends Commit with wrong finalAmount (forced) → receiver returns 409 → sender rolls back.
- `wf_interbank_incoming_success_test.go` — test-app acts as sender calling this bank's `/internal/inter-bank/*` with a valid HMAC; verify the receiver flow end-to-end.
- `wf_interbank_hmac_rejection_test.go` — bad signature, unknown code, stale timestamp, replay nonce → each returns the correct 401.
- `wf_interbank_receiver_abandoned_test.go` — test-app sends a Prepare, gets Ready, never sends Commit; after `receiver_commit_wait_seconds` the receiver row is `abandoned` and funds unreserved.
- `wf_interbank_crash_recovery_test.go` — sender row is hand-inserted in `committing` with old `updated_at`; restart transaction-service (simulated via explicit recovery call); verify it runs the reconciler path.

### 12.3 Helpers

Added to `test-app/workflows/helpers_interbank_test.go`:
- `StartMockPeerBank(t, config) *MockPeerBank` — spins up the mock HTTP server.
- `SeedBanksTable(t, code, baseUrl, apiKey)` — DB utility.
- `ScanInterBankStatusKafka(t, tx_id) []string` — reads events for a tx.
- `PollTransferStatus(t, tx_id, want string, timeout) *TransferView` — polls `/api/v1/me/transfers/{id}`.

### 12.4 Non-negotiables

- Every new route (7.1 and 7.2) MUST have at least one unit test and at least one integration test.
- Every state-machine transition (section 5) MUST be exercised by at least one test.
- All four Kafka topics (section 10) MUST be covered by an `EnsureTopics` smoke test (topic exists after startup) and by at least one end-to-end test that reads the event.
- `make test` and `make lint` MUST pass before merge.

## 13. Spec / REST Doc Updates

When this spec is implemented, the following docs MUST be updated in the same PR:

- `Specification.md`:
  - Section 3 (gateway client wiring): add HMACMiddleware and the `/internal/inter-bank/*` routes.
  - Section 11 (gRPC services): add `InterBankService` methods in transaction-service.
  - Section 17 (API routes): add the extended `POST /api/v1/transfers` semantics and the three internal endpoints.
  - Section 18 (entities): add `banks` and `inter_bank_transactions`.
  - Section 19 (Kafka topics): add the four `transfer.interbank-*` topics.
  - Section 20 (enums): add `role`, `phase`, and full `status` enums from section 5.3.
  - Section 21 (business rules): add the 2PC rules and the bank-prefix routing rule.
- `docs/api/REST_API_v1.md`:
  - Extend the transfer section with inter-bank response codes and poll-url.
  - Add the new "Internal inter-bank endpoints" section.
- `api-gateway/docs/` (swagger): regenerate via `make swagger`.
- `docker-compose.yml` and `docker-compose-remote.yml`:
  - Add env vars to transaction-service: `PEER_222_BASE_URL`, `PEER_222_INBOUND_KEY`, `PEER_222_OUTBOUND_KEY`, and the same for `333`/`444`.
  - Add env vars to api-gateway: the same peer config for the HMAC middleware.
  - No new services, DBs, or volumes — transaction-service's existing DB gets the new tables via auto-migrate.

## 14. Out of Scope

- **Cross-bank OTC securities settlement.** Spec 4 extends this transport layer with additional `action` values (`ReserveShares`, `ReserveSharesConfirm`, `ReserveSharesFail`, `CommitFunds`, `TransferOwnership`) and a longer-lived saga coordinating shares and funds across both banks. This spec reserves those action names and requires the action dispatcher be extensible, but does not implement them.
- **Admin CRUD for `banks` table.** In v1, the registry is seeded from env + migrations. A future spec may add `/api/v1/admin/banks/*` with a new `interbank.admin` permission.
- **Key rotation with overlap.** v1 is single-key; rotation is a restart.
- **Signed envelopes / mTLS.** The `public_key` column is reserved for this but unused.
- **Multi-hop routing.** v1 assumes each bank has a direct connection to every bank whose accounts its clients want to pay. The Celina 5 doc explicitly allows a mesh where not every pair is connected, but the project target is direct pairs only.
- **Currency pair support for exotic currencies.** The receiver applies whatever conversion exchange-service supports. Unsupported → NotReady with `currency_not_supported`.
- **Settlement windows / batching.** Every transfer is executed individually in real time; there is no end-of-day batch.
- **Chargebacks, disputes, reversals.** Out of Celina 5 scope entirely.

## 15. Open Questions

1. **Cross-currency slippage on fees:** if sender sends 100 USD and receiver's bank charges 15% fees, the sender loses 100 USD and the receiver's account gets 85 USD equivalent. Is that the correct user-visible behavior, or should the sender be warned at UI time?
   - **Default answer:** this is correct per the doc (receiver computes FX + fees at Prepare, sender locks both in the Ready response). UI warning is a frontend concern.

2. **Peer unreachable at Prepare:** when bank B is entirely down and we can't even complete the Prepare HTTP, we return to the client relatively quickly (30 seconds). Does that leave the user's funds reserved for 30 seconds unnecessarily? Could we release funds earlier?
   - **Default answer:** no. The reservation is needed in case the Prepare did arrive at the peer (network loss on the response leg). We must keep funds reserved until the reconciler can confirm.

3. **Replay attacks on Prepare:** an attacker with a valid captured HMAC could replay an old Prepare. Our defenses: (a) nonce tracking in Redis, (b) timestamp window ±5 min, (c) idempotency on tx_id — a replayed Prepare returns the original state, not re-validating. Is this sufficient for the academic-project threat model?
   - **Default answer:** yes.

4. **Bank registry updates:** when a peer's `base_url` changes, how does this bank learn? v1 requires manual DB update + restart. Is that acceptable for the faculty project?
   - **Default answer:** yes. Frequency is expected to be rare.

5. **Partial Ready honor:** can the sender ignore the Ready and abort on its own (e.g., user cancels between Ready and Commit)? The Celina 5 doc doesn't describe a "client cancel" path. If we allow it, we need a **Release** message the sender can send to the receiver to free the receiver's incoming reservation.
   - **Default answer:** not in v1. Once the client submits the transfer, they commit to the full 2PC. If user-cancel is later needed, a new action `Release` can be added in a follow-up spec (Spec 4 or later).

6. **Account number uniqueness across banks:** bank 222's account `2220000000001234` and bank 111's account `1110000000001234` are unambiguous because of the prefix. But an attacker could submit `2220000000001234` when bank 222's row is inactive in our `banks` table — what's the correct response?
   - **Default answer:** 400 `unknown_bank`. This is a validation failure at api-gateway, not a routing error.

7. **Receiver-side limits:** do the receiver's per-client daily/monthly spending limits apply to **incoming** transfers? Intuition says no (limits apply to outgoing spending), but the doc is silent.
   - **Default answer:** no. Incoming transfers credit the client; limits don't apply. If AML/KYC-style limits on incoming need to exist, they belong in a later spec.

8. **Fee collection on receiver side:** the receiver bank's fees are deducted at Prepare-time (reflected in `finalAmount < originalAmount × fxRate`). The deducted amount is credited to the receiver bank's own bank account. Does that credit happen immediately at CommitIncoming, or during Prepare's reservation?
   - **Default answer:** at CommitIncoming, via a second ledger entry in the same DB transaction. Reservation holds the gross amount; commit splits it into client-credit + bank-fee-credit.

9. **Sender-side fees:** does this bank charge its **own** sender clients a fee for outgoing inter-bank transfers, separate from the peer's fees? The existing `transfer_fees` table supports this.
   - **Default answer:** yes — existing `transfer_fees` rules apply on the sender side uniformly. Those fees are charged to this bank's own client and credited to this bank's own RSD account. The peer's fees are additional and are charged against the amount as received at the peer.

10. **Hooks for Spec 4 (cross-bank OTC):** the action dispatcher in section 6.1 must be extensible without modifying the existing three actions' logic. The `inter_bank_transactions` table may need an additional `kind` enum column (`payment` vs `otc_settlement`) before Spec 4 lands, but we can defer that to Spec 4's migration rather than pre-speccing it now.
    - **Default answer:** add `kind` column later, in Spec 4's migration. Keep the door open by reserving the column name.

---

*End of spec. Implementation plan should follow in `docs/superpowers/plans/2026-04-NN-interbank-2pc-transfers.md`.*
