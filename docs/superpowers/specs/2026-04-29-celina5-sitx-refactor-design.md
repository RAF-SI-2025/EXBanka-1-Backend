# Celina 5 SI-TX Refactor — Design

**Date:** 2026-04-29
**Status:** approved (awaiting implementation plan)
**Owner:** lukasavic

## Background

Celina 5 of the faculty banking-systems specification (`docs/Celina 5(Nova).docx.md`) describes inter-bank communication for two flows: cross-bank transfers (Plaćanja) and cross-bank OTC option trading (OTC Trgovina). The Celina 5 document defers all wire-protocol details to the cohort spec at `https://arsen.srht.site/si-tx-proto/`.

The current backend implementation (catalogued in `Specification.md` §25 and §27) does **not** match the SI-TX cohort wire protocol. The team's implementation invented its own headers (`X-Bank-Code` + `X-Bank-Signature` HMAC + `X-Idempotency-Key` + `X-Timestamp` + `X-Nonce`), endpoint shapes (three split routes `/internal/inter-bank/transfer/prepare` + `/transfer/commit` + `/check-status`), action enum (`Prepare` / `Ready` / `NotReady` / `Commit` / `Committed` / `CheckStatus` / `Status`), and a status state-machine + reconciler cron. Approximately 8000 lines of code across `api-gateway`, `transaction-service`, `stock-service`, and `contract` follow these wrong assumptions.

This design refactors all wrong-assumption code so cross-bank communication conforms to SI-TX while preserving the team's hybrid auth scheme as house policy.

## Goals

1. Bring all Celina 5 wire formats into compliance with SI-TX (single `POST /interbank` endpoint, `Message<Type>` envelope, `NEW_TX` / `COMMIT_TX` / `ROLLBACK_TX` action enum, `TransactionVote` with the 8 SI-TX `NoVote` reasons).
2. Add the SI-TX-mandated peer-facing OTC endpoints currently missing (`GET /public-stock`, `POST/PUT/GET/DELETE /negotiations/{rid}/{id}`, `GET /negotiations/{rid}/{id}/accept`, `GET /user/{rid}/{id}`).
3. Provide hybrid auth: gateway accepts either strict-SI-TX `X-Api-Key` alone or the team's HMAC bundle. Peers self-select based on what they support.
4. Delete every wrong-assumption Celina 5 file, table, and Kafka topic — leave the repo with one inter-bank protocol implementation, not two.
5. Keep all reusable Celina 5 plumbing that matches SI-TX shapes (notably `account-service.IncomingReservation` and the `ReserveIncoming` / `CommitIncoming` / `ReleaseIncoming` RPCs).

## Non-goals

- This refactor does not change intra-bank behaviour for `POST /api/v3/me/transfers` — only the inter-bank dispatch path.
- It does not redesign `account-service`'s posting / ledger model.
- It does not introduce a separate microservice for peer-bank protocol; everything lives on existing services.
- It does not migrate in-flight `inter_bank_transactions` data — those tables are dropped cold.

## Architecture

**Service placement.** All SI-TX peer-facing endpoints live on **api-gateway** under the `/api/v3/` prefix:
- `POST /api/v3/interbank` — TX envelope (`NEW_TX`, `COMMIT_TX`, `ROLLBACK_TX`).
- `GET /api/v3/public-stock` — peer OTC discovery.
- `POST /api/v3/negotiations`, `PUT /api/v3/negotiations/{rid}/{id}`, `GET /api/v3/negotiations/{rid}/{id}`, `DELETE /api/v3/negotiations/{rid}/{id}`, `GET /api/v3/negotiations/{rid}/{id}/accept` — OTC negotiation lifecycle.
- `GET /api/v3/user/{rid}/{id}` — user information lookup; called by peers when displaying counterparty identity in OTC negotiations or transfer history.

**Admin CRUD over peer banks** — also on api-gateway, **not** peer-facing:
- `GET /api/v3/peer-banks` — list registered peer banks.
- `GET /api/v3/peer-banks/:id` — read one.
- `POST /api/v3/peer-banks` — register a new peer (bank code, routing number, base URL, API token, optional HMAC keys, active flag).
- `PUT /api/v3/peer-banks/:id` — update mutable fields (base URL, API token, HMAC keys, active flag).
- `DELETE /api/v3/peer-banks/:id` — remove a peer.
These routes use `AuthMiddleware` (employee JWT) + `RequirePermission("peer_banks.manage")`. New permission, granted to `EmployeeAdmin` only. Lets ops add/remove peer banks at runtime without redeploy or DB-poke; supports the user-facing requirement to "edit which banks we work with."

**Routing number ↔ bank code.** SI-TX `routingNumber` is a numeric identifier; the team's existing `bank_code` is a 3-digit string ("111", "222", "333", "444"). The two are **the same value, different encodings.** `peer_banks.routing_number` stores the int form (`111`); `peer_banks.bank_code` stores the string form (`"111"`). Postings reference `routingNumber` (int); legacy code paths reference `bank_code` (string). Conversion is `strconv.Itoa` / `strconv.Atoi` — no semantic difference.

Peer banks configure each other's base URLs as `https://exbanka.example/api/v3` and concatenate SI-TX paths against it. SI-TX paths are relative to a per-peer base URL, so the `/api/v3/` prefix does not violate conformance.

Client-facing (`/api/v3/me/...`) and peer-facing (`/api/v3/interbank` etc.) routes share the prefix but differ by middleware. Client routes use `AnyAuthMiddleware` (JWT). Peer routes use the new `PeerAuth` middleware.

**Dispatch model.** `peer_tx_handler` decodes `Message<Type>` and dispatches by `messageType`:
- `NEW_TX`, `COMMIT_TX`, `ROLLBACK_TX` → `transaction-service.PeerTxService` via gRPC.
- `/public-stock` and `/negotiations/...` → `stock-service.PeerOTCService` via gRPC.
- `/user/{rid}/{id}` → `user-service` (employees) or `client-service` (clients), routed by `rid` (system_type discriminator).

**No new microservice.** A dedicated `peer-bank-service` was considered and rejected — it adds an extra gRPC hop without separating concerns the gateway can't already handle.

## Components

### Hybrid auth middleware

**`api-gateway/internal/middleware/peer_auth.go`** — new. Replaces `hmac.go`. Single `PeerAuth(peerBanks PeerBankRepo, idempStore IdempotenceStore, hmacWindow time.Duration)` constructor.

Branch logic:
1. If `X-Bank-Signature` header present → HMAC path: verify timestamp ± `hmacWindow`, nonce uniqueness in Redis, body HMAC against `peer_banks.hmac_inbound_key`.
2. Else → API-token path: look up `X-Api-Key` against `peer_banks.api_token`, constant-time compare.

Both paths populate `c.Set("peer_bank_code", ...)` and `c.Set("peer_routing_number", ...)` so downstream handlers don't care which path was taken. Failure → 401 with empty body, no info leak.

**Outbound auth selection.** When transaction-service or stock-service makes a request *to* a peer, mode is determined per-peer from the `peer_banks` row: if `hmac_outbound_key` is non-null, send the full HMAC bundle (`X-Bank-Code` + `X-Bank-Signature` + `X-Timestamp` + `X-Nonce`) plus `X-Api-Key`. Otherwise send only `X-Api-Key`. The peer's `PeerAuth` middleware will accept whichever set we send.

### Peer-facing handlers (api-gateway)

- **`peer_tx_handler.go`** — `POST /api/v3/interbank`. Decodes envelope, validates `idempotenceKey` shape, dispatches to `transaction-service.PeerTxService`.
- **`peer_otc_handler.go`** — 6 OTC routes. Dispatches to `stock-service.PeerOTCService`.
- **`peer_user_handler.go`** — `GET /api/v3/user/{rid}/{id}`. Routes to `user-service` or `client-service` by `rid`.
- **`peer_bank_admin_handler.go`** — admin CRUD on `peer_banks`. 5 routes (list / get / create / update / delete). Calls `transaction-service.PeerBankAdminService` via gRPC.

### gRPC contract changes

**`contract/proto/transaction/transaction.proto`** — replaces `InterBankService` with `PeerTxService`:

```
service PeerTxService {
  rpc HandleNewTx(SiTxNewTxRequest) returns (SiTxVoteResponse);
  rpc HandleCommitTx(SiTxCommitRequest) returns (google.protobuf.Empty);
  rpc HandleRollbackTx(SiTxRollbackRequest) returns (google.protobuf.Empty);
  rpc InitiateOutboundTx(InitiateOutboundRequest) returns (InitiateOutboundResponse);
}

service PeerBankAdminService {
  rpc ListPeerBanks(ListPeerBanksRequest) returns (ListPeerBanksResponse);
  rpc GetPeerBank(GetPeerBankRequest) returns (PeerBank);
  rpc CreatePeerBank(CreatePeerBankRequest) returns (PeerBank);
  rpc UpdatePeerBank(UpdatePeerBankRequest) returns (PeerBank);
  rpc DeletePeerBank(DeletePeerBankRequest) returns (google.protobuf.Empty);
}
```

The four sender-side ↔ receiver-side `InterBankService` RPCs (`InitiateInterBankTransfer`, `HandlePrepare`, `HandleCommit`, `HandleCheckStatus`, `GetInterBankTransfer`) are deleted. `ReverseInterBankTransfer` is also deleted (no compensating-transfer concept in SI-TX — rollback is a `ROLLBACK_TX` message, not a separate reversed TX).

**`contract/proto/stock/stock.proto`** — adds `PeerOTCService` (6 RPCs matching the 6 OTC routes). Drops `CrossBankOTCService` (12 RPCs deleted).

**`contract/sitx/types.go`** — new package. Pure-Go SI-TX wire types:

```go
package sitx

type IdempotenceKey struct {
    RoutingNumber       int64  `json:"routingNumber"`
    LocallyGeneratedKey string `json:"locallyGeneratedKey"`
}

type Message[T any] struct {
    IdempotenceKey IdempotenceKey `json:"idempotenceKey"`
    MessageType    string         `json:"messageType"`
    Message        T              `json:"message"`
}

type Posting struct {
    RoutingNumber int64           `json:"routingNumber"`
    AccountID     string          `json:"accountId"`
    AssetID       string          `json:"assetId"`
    Amount        decimal.Decimal `json:"amount"`
    Direction     string          `json:"direction"` // "DEBIT" | "CREDIT"
}

type Transaction struct {
    Postings []Posting `json:"postings"`
}

type CommitTransaction struct {
    TransactionID string `json:"transactionId"`
}

type RollbackTransaction struct {
    TransactionID string `json:"transactionId"`
}

type NoVote struct {
    Reason  string  `json:"reason"`  // one of the 8 SI-TX codes
    Posting *int    `json:"posting,omitempty"` // index into postings, when applicable
}

type TransactionVote struct {
    Type    string   `json:"type"`              // "YES" | "NO"
    NoVotes []NoVote `json:"noVotes,omitempty"`
}

// + OtcOffer, OptionDescription, OtcNegotiation, UserInformation,
// + PublicStocksResponse, ForeignBankId
```

Used by both gateway (decoding inbound) and downstream services (re-encoding for replay cache).

### Service-internal packages

**`transaction-service/internal/sitx/`** — new:
- `dispatcher.go` — receives `Message[Transaction]`, validates postings balance, routes to `posting_executor.go`.
- `posting_executor.go` — translates SI-TX postings into existing `account-service` calls. Money postings (currency `assetId`s) → `UpdateBalance` / `ReserveIncoming` / `CommitIncoming`. Stock postings (`OptionDescription` `assetId`) → calls into `stock-service`.
- `vote_builder.go` — produces `TransactionVote` with the 8 SI-TX `NoVote` reasons.
- `peer_idempotence_repo.go` — composite-unique `(peer_bank_code, locally_generated_key)` lookup with replay-cache return.
- `peer_bank_repo.go` — replaces deleted `bank_repository.go`.
- `outbound_peer_tx_repo.go` — sender-side state for retry / replay cron.
- `outbound_replay_cron.go` — 30s tick, scans `outbound_peer_txs` rows in `pending` older than 60s, re-POSTs `Message<Transaction>` with same idempotence key.

**`stock-service/internal/sitx/`** — new:
- `negotiation_handler.go` — backs the 5 `/negotiations/...` RPCs.
- `tx_handler.go` — handles `OptionDescription` postings inside `NEW_TX` (called from transaction-service when a TX has stock postings).
- `public_stocks_repo.go` — backs `GET /public-stock`.

**`account-service`** — no new code. `IncomingReservation` + `ReserveIncoming` / `CommitIncoming` / `ReleaseIncoming` survive — already SI-TX-shape-compatible. Reservation `reservation_key` becomes `<peer_bank_code>:<locally_generated_key>`.

## Database schema

**Add:**
- `peer_banks (id, bank_code, routing_number, base_url, api_token, hmac_inbound_key, hmac_outbound_key, active, created_at, updated_at)` — one row per peer. Seeded from `seeder/peer_banks.sql`. Source of truth for peer secrets; env vars are eliminated.
- `peer_idempotence_records (id, peer_bank_code, locally_generated_key, tx_id, response_payload_json, created_at)` — composite unique `(peer_bank_code, locally_generated_key)`. Receiver-side replay cache. Retained indefinitely per SI-TX §"R must record the idempotence key" — sender may re-POST the same key arbitrarily long after the original TX. A separate ops cleanup (out of scope) can prune rows older than 12 months.
- `peer_otc_negotiations (id, rid, foreign_id, offer_json, last_modified_by_principal_type, last_modified_by_principal_id, status, created_at, updated_at)` — receiver-side persistence of inbound OTC negotiations from peer banks.
- `outbound_peer_txs (id, idempotence_key, peer_bank_code, tx_kind, postings_json, status, attempt_count, last_attempt_at, created_at)` — sender-side state for retry/replay cron. Rows in terminal status (`committed` / `rolled_back` / `failed`) retained 30 days for audit, then pruned by a daily cleanup goroutine inside transaction-service.

**Drop:**
- `banks` (replaced by `peer_banks`).
- `inter_bank_transactions` (state machine no longer applies).
- `inter_bank_saga_logs` (no cross-bank saga replay needed; idempotence-key replay covers it).
- The `idempotency_key` column on existing tables that held HMAC `X-Idempotency-Key` values (replaced by SI-TX `(routingNumber, locallyGeneratedKey)`).

**Keep:**
- `incoming_reservations` (account-service).

## Data flow

### Sender side — outbound transfer

1. `POST /api/v3/me/transfers` arrives at api-gateway with foreign-prefix receiver (3-digit prefix ≠ `OWN_BANK_CODE`).
2. Gateway calls `transaction-service.PeerTxService.InitiateOutboundTx`.
3. Transaction-service:
   - Generates `IdempotenceKey { routingNumber: OWN_ROUTING, locallyGeneratedKey: uuid }`.
   - Builds `Transaction` with two postings (debit sender, credit peer's receiver).
   - Persists `outbound_peer_txs` row in `pending`.
   - Debits sender immediately via `UpdateBalance(amount=-X, idempotency_key="peer-out-debit-<idem>")`. Sender-debit-immediate semantics preserved from current implementation.
   - HTTP `POST {peer_base_url}/interbank` with `Message<Transaction>`.
4. Peer responds:
   - `200 + TransactionVote{type: YES}` → row → `committing`. Send `Message<CommitTransaction>` via second `POST /interbank`. On peer 200/204 → row → `committed`. Publish `transfer.completed` Kafka.
   - `200 + TransactionVote{type: NO}` → credit sender back via `UpdateBalance(amount=+X, idempotency_key="peer-out-creditback-<idem>")`. Row → `rolled_back`. Publish `transfer.rolled-back` Kafka with reasons.
   - `202` → row stays `pending`. `OutboundReplayCron` re-POSTs after 60s. Receiver returns cached vote on replay.
   - HTTP error / timeout → same retry path. 4-attempt cap (60s, 120s, 240s, 480s). On give-up, row → `failed`. Publish error Kafka.

### Receiver side — inbound `NEW_TX`

1. `PeerAuth` middleware validates auth → sets `peer_bank_code`.
2. `peer_tx_handler` decodes envelope. Looks up `(peer_bank_code, locally_generated_key)` in `peer_idempotence_records`:
   - Hit → return cached `response_payload_json`. No further work.
   - Miss → forward to `transaction-service.PeerTxService.HandleNewTx`.
3. Transaction-service:
   - Validates postings balance (Σ debits = Σ credits per `assetId`).
   - Per-posting checks → builds `TransactionVote`. NoVote reasons:
     - `UNBALANCED_TX` — postings don't balance.
     - `NO_SUCH_ACCOUNT` — `accountId` does not exist locally.
     - `NO_SUCH_ASSET` — `assetId` not recognised (currency / `OptionDescription`).
     - `UNACCEPTABLE_ASSET` — asset cannot be credited/debited to that account.
     - `INSUFFICIENT_ASSET` — credit/debit exceeds balance.
     - `OPTION_AMOUNT_INCORRECT` — option posting amounts don't match contract.
     - `OPTION_USED_OR_EXPIRED` — option already exercised or past settlement date.
     - `OPTION_NEGOTIATION_NOT_FOUND` — referenced negotiation does not exist.
4. On YES: reserve resources atomically — `account-service.ReserveIncoming(reservation_key="<peer_bank_code>:<idem>")` for credit postings; `HoldingReservation` for stock postings via stock-service.
5. **Same DB transaction:** insert `peer_idempotence_records` with `response_payload_json` = serialised `TransactionVote{YES}`. Commit.
6. Return 200 + body.

### Receiver side — inbound `COMMIT_TX` / `ROLLBACK_TX`

`COMMIT_TX`: `account-service.CommitIncoming` finalises credit postings; stock-service consumes the `HoldingReservation` and transfers ownership. Update `peer_idempotence_records` with new payload (empty 204 ack). Publish `transfer.received` Kafka. Return 204.

`ROLLBACK_TX`: `account-service.ReleaseIncoming` releases reservation; stock-service releases holding reservation. Update idem record with 204 payload. Return 204.

### Cross-bank OTC

Two phases on the same SI-TX surface:

**Negotiation lifecycle (REST CRUD on `/negotiations/...`):** Buyer-bank POSTs `OtcOffer` to seller-bank's `POST /api/v3/negotiations`. Seller-bank's stock-service stores in `peer_otc_negotiations`, returns `ForeignBankId`. Subsequent counter-offers via `PUT /api/v3/negotiations/{rid}/{id}` alternate ownership per SI-TX rule (`isOngoing iff offer.lastModifiedBy ≠ offer.buyerId`). Either side `DELETE`s to cancel.

**Acceptance triggers TX:** Acceptor `GET`s `/api/v3/negotiations/{rid}/{id}/accept` on the other bank. The receiving bank composes the option-formation `Transaction` with 4 postings (premium money + 1× `OptionDescription` both directions) and runs the standard `NEW_TX` flow. SI-TX models this as a *single* TX with cross-bank postings, not two coordinated TXs — both banks process the same `Message<Transaction>`, each evaluates whether it can satisfy the postings touching its own accounts, and votes YES or NO. The acceptor side performs both votes (its own + sends `NEW_TX` to the other bank for theirs).

**Partial-failure rollback (cross-bank OTC).** SI-TX does not specify a coordinated rollback when the two sides vote differently; the acceptor-side TX is treated atomically. Concretely: the acceptor sends `NEW_TX` to the other bank. If the other bank votes NO, the acceptor abandons its own pending reservation (no `COMMIT_TX` ever sent → reservation auto-times-out and gets released by an existing `account-service` cron). If the acceptor side fails its own pre-checks before sending `NEW_TX`, no peer call is made and the negotiation stays open. There is no compensating TX in the SI-TX model.

## Error handling

**HTTP status code matrix (per SI-TX §"Error Semantics"):**

| Status | Meaning | Sender behaviour |
|---|---|---|
| 200 | Body parsed (`TransactionVote` or REST response) | Process body |
| 202 | Logged, processing | Retry via `OutboundReplayCron` |
| 204 | Terminal success | Done |
| 4xx, 5xx, network error | Retry | Exponential backoff up to 4 attempts |

After 4 attempts → row `failed`, Kafka error event, no further auto-retry. Manual ops intervention.

**`TransactionVote.NO` reasons** — see Data flow §3 for the 8 SI-TX codes. All NO votes are hard-fail on sender side: credit-back, release reservations, Kafka publish, return error to user. No charitable retry (sender's pre-checks should have caught it).

**Idempotence-key replay** — receiver guarantees identical `response_payload_json` for any same `(peer_bank_code, locally_generated_key)`. Sender retries are safe by construction.

**Authentication failure** — both auth paths return 401 with empty body. Constant-time compare for both `X-Api-Key` and HMAC. No info leak about which path was attempted, which header failed, or whether the bank code is registered.

**Crash recovery:**
- Receiver crash mid-handler before `peer_idempotence_records` insert → no record, peer retry re-runs cleanly.
- Receiver crash after insert but before HTTP response → next retry returns cached vote, peer is happy, no double-execution.
- Sender crash mid-flight → `OutboundReplayCron` resumes from `outbound_peer_txs` `pending` rows older than 60s.

## Testing

### Unit tests (per service)

- `transaction-service/internal/sitx/posting_executor_test.go` — every NoVote path, balanced/unbalanced TX, multi-asset postings, replay-cache hit/miss.
- `transaction-service/internal/sitx/peer_idempotence_repo_test.go` — composite-key uniqueness, response cache replay.
- `transaction-service/internal/sitx/outbound_replay_cron_test.go` — retry cap, backoff schedule, give-up condition.
- `api-gateway/internal/middleware/peer_auth_test.go` — both auth paths happy / failure, 401 paths, missing-header combinations.
- `api-gateway/internal/handler/peer_tx_handler_test.go` — envelope decoding, dispatch routing, status-code emission.
- `api-gateway/internal/handler/peer_otc_handler_test.go` — all 6 routes, parameter validation, error mapping.

### Integration tests (`test-app/workflows/`)

Replace existing inter-bank workflows with SI-TX equivalents:

- `wf_peer_tx_success_test.go` — happy path NEW_TX → YES → COMMIT_TX → 204.
- `wf_peer_tx_novote_test.go` — each of the 8 NoVote reasons triggers correct sender-side rollback.
- `wf_peer_tx_idempotence_test.go` — replay returns cached vote (no double-execution).
- `wf_peer_tx_rollback_test.go` — explicit ROLLBACK_TX after partial accept.
- `wf_peer_auth_test.go` — both auth paths accepted; combinations of missing headers all return 401.
- `wf_peer_tx_crash_recovery_test.go` — sender crash; replay cron resumes; receiver returns cached vote.
- `wf_peer_otc_negotiation_test.go` — full negotiate → counter → accept → dual TX flow.

Files deleted: `wf_interbank_*` (8 files), `helpers_interbank_test.go`, `crossbank_otc_test.go`, `wf_crossbank_saga_durability_test.go`. Replaced by the above.

### Cohort dry-run

`test-app/workflows/cohort_dry_run_test.go` — gated behind `COHORT_DRY_RUN_PEER` env var. Skipped by default. One success case + one NoVote case against another faculty team's actual bank.

## Phased execution

Per Approach 3 from brainstorming. Each phase ends in a `main`-mergeable state.

**Phase 1 — Demolition.** Delete all wrong-assumption code. Drop schema tables. `POST /api/v3/me/transfers` falls back to intra-bank only; inter-bank prefix returns `501 Not Implemented`. Test-app inter-bank workflows skipped with `t.Skip("pending SI-TX")`. `Specification.md` §25/§27 replaced with a "pending SI-TX implementation" stub. Repository layout: ~8000 LOC removed, no new code yet.

**Phase 2 — SI-TX foundation.** Add `contract/sitx/types.go`, `peer_auth` middleware, `peer_banks` table + seeder, `peer_idempotence_records` table. Add stub `POST /api/v3/interbank` that 501s. Hybrid auth working; envelope decoding working. **Also adds the admin CRUD surface** — `PeerBankAdminService` gRPC, the 5 `/api/v3/peer-banks` routes, and the new `peer_banks.manage` permission seeded for `EmployeeAdmin`. After Phase 2 admins can register peers via REST without redeploys.

**Phase 3 — TX execution (transfers).** Implement `posting_executor` → wire to existing `account-service` RPCs. Implement `vote_builder` with all 8 NoVote reasons. Wire `POST /api/v3/me/transfers` inter-bank dispatch. `outbound_peer_txs` table + `OutboundReplayCron`. Re-enable `wf_peer_tx_*` workflows.

**Phase 4 — OTC negotiations.** Add `PeerOTCService` gRPC. Wire 6 OTC peer routes. `peer_otc_negotiations` table. Wire stock-service cross-bank dispatch to call peer's `/negotiations` instead of in-house. Re-enable `wf_peer_otc_negotiation_test.go`.

**Phase 5 — Specs + docs.** Rewrite `Specification.md` §25/§27 to describe SI-TX. Add SI-TX endpoints to `REST_API_v3.md` under a new "Peer-Bank Protocol" section. Frontend-facing notes on `POST /api/v3/me/transfers` updated for `202` polling.

**Phase 6 — Integration & cohort dry-run.** End-to-end test against local mock peer covering: NEW_TX accepted → COMMIT_TX → success; NEW_TX rejected with each NoVote reason; ROLLBACK_TX after partial failure; idempotence-key replay returns cached vote; OTC offer → counter → accept saga.

## Decisions baked in

These were settled during brainstorming and are not open questions:

- **Auth:** hybrid. `PeerAuth` middleware accepts either `X-Api-Key` alone or the full HMAC bundle.
- **Reconciliation:** dropped. SI-TX defines none. Recovery is sender-side `OutboundReplayCron` re-POSTing same idempotence key; receiver returns cached vote.
- **Schema:** drop `inter_bank_transactions`, `inter_bank_saga_logs`, `banks` cold. No data migration.
- **Peer secrets:** DB as source of truth (`peer_banks` table). Env vars eliminated. Seeded from `seeder/peer_banks.sql`.
- **Sender-debit-immediate:** preserved. Existing semantic where outbound transfer debits sender's account before sending NEW_TX, credits back on rollback.
- **Retry policy:** 4 attempts, 60s/120s/240s/480s exponential backoff, then mark `failed`.
- **NoVote semantics:** all hard-fail. Sender does not retry NO votes regardless of reason.
- **Peer-bank admin CRUD:** runtime-editable via 5 REST routes under `/api/v3/peer-banks`, gated by new `peer_banks.manage` permission (EmployeeAdmin). Seed file `seeder/peer_banks.sql` provides initial registrations; admins can add/update/remove peers without redeploy.

## Open follow-ups (out of scope for this design)

- HMAC-mode peer key rotation policy.
- Cohort agreement on which auth mode each peer prefers.
- Frontend updates for `202 Accepted` + polling URL response (the FE currently expects synchronous `201` for inter-bank case — will need a separate FE plan).
