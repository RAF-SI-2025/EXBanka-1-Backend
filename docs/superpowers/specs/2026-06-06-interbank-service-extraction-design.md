# interbank-service — Cross-Bank SI-TX Settlement Engine (Standalone Service)

**Date:** 2026-06-06
**Status:** Approved (design)
**Scope:** Build the service **fully** as a parallel, dormant drop-in. Integration into the gateway / stock-service / transaction-service is a **separate later task** (the user cannot deploy new services / change Helm right now).

## Purpose

Extract the cross-bank **SI-TX 2PC settlement engine** out of transaction-service into a standalone gRPC service, `interbank-service`. It owns:

- the SI-TX transaction-execution transport (NEW_TX / COMMIT_TX / ROLLBACK_TX 2PC, voting, idempotency, replay/reconcile recovery),
- the **peer-bank registry** (`peer_banks`) and peer resolution/auth,
- **all permitted outbound HTTP egress to peer banks** — both its own typed `/interbank` calls AND a generic signed-proxy gRPC for the OTC/discovery calls other services make.

It implements the **existing** `transactionpb.PeerTxService` + `PeerBankAdminService` (no contract change to those), so integration is just re-pointing the gateway's gRPC client address.

## Hard constraints (from the user)

- **api-gateway is the only HTTP↔gRPC translator.** interbank-service exposes **gRPC only** for business. Its **only** HTTP is ops: `/metrics` (Prometheus/Grafana), `/healthz`, `/readyz` (k8s probes). No business REST in or out via its own HTTP server — *except* it is the component that makes outbound HTTP **to peer banks** (egress), which is inherent to SI-TX.
- **Dormant build.** No docker-compose / docker-compose-remote / Helm / gateway / stock-service / transaction-service wiring changes. Nothing in the running system changes.

## Architecture

```
interbank-service/  (module github.com/exbanka/interbank-service, added to go.work)
  cmd/main.go              — load config; AutoMigrate; dial account+stock; start gRPC :50062;
                             start ops HTTP :9108; start replay + reconcile crons; graceful shutdown
  internal/config/         — env → Config
  internal/model/          — PeerBank, PeerIdempotenceRecord, OutboundPeerTx        (ported verbatim)
  internal/repository/     — PeerBank/PeerIdempotence/OutboundPeerTx repos          (ported verbatim)
  internal/sitx/           — PostingExecutor, PeerHTTPClient, Vote, BuildPrelimVote (ported verbatim)
  internal/handler/        — PeerTxGRPCHandler, PeerBankAdminGRPCHandler            (ported; GetTxStatus fix included)
                           — PeerEgressGRPCHandler                                 (NEW: generic signed proxy)
  internal/service/        — OutboundReplayCron, PeerTxReconciler                   (ported verbatim)
  internal/cronreg/        — cron registry                                         (ported if not shareable)
  internal/grpcmw/         — saga-context client interceptor                        (ported if not shareable)
  internal/metrics/        — Prometheus registry + handler
  internal/health/         — liveness/readiness handlers
  Dockerfile
```

### gRPC surface — interbank-service is the SINGLE backend for the WHOLE `/cross-bank-protocol`
The api-gateway routes *every* `/cross-bank-protocol/*` request to interbank-service; it is the protocol coordinator/engine. It serves these natively and **forwards the domain ones** to their owners (OTC → stock-service, /user → client/user-service), so those domains stay where they belong while interbank-service is the one inbound boundary.

- `transactionpb.PeerTxService` — HandleNewTx, HandleCommitTx, HandleRollbackTx, InitiateOutboundTx, InitiateOutboundTxWithPostings, GetTxStatus. (own engine; reused proto)
- `transactionpb.PeerBankAdminService` — List/Get/Create/Update/Delete PeerBank, ResolvePeerByAPIToken, ResolvePeerByBankCode. (own registry; reused proto)
- `stockpb.PeerOTCService` — GetPublicStocks, GetPublicOptionOffers, Create/Update/Get/Delete/Accept Negotiation. **Implemented as a transparent forwarder to stock-service** (the OTC domain owner). The internal option-leg RPCs stay Unimplemented here (interbank CALLS those on stock during settlement; the gateway never invokes them on interbank).
- **NEW** `transactionpb.PeerUserService.ResolvePeerUser` — the SI-TX `/user/{rid}/{id}` friendly-name lookup; forwards to client-service/user-service and composes the display name (own-routing gated, NotFound-tolerant).
- **NEW** `transactionpb.PeerEgressService`:
  - `CheckPeerReachability(peer_bank_code) → PeerReachability` — signed `GET /public-stock` probe of ONE peer; reports `{reachable, status_code, latency_ms, error, checked_at, active, base_url, routing}`. Powers the admin "verify on add" flow (the caller chains CreatePeerBank → CheckPeerReachability; registration itself stays a pure DB write).
  - `GetPeersState() → [PeerReachability]` — probes ALL registered peers concurrently (per-peer bounded timeout); the cross-peer fleet health view.
  - `ProxyToPeer(ProxyToPeerRequest) returns (ProxyToPeerResponse)`:
  - `ProxyToPeerRequest { string peer_bank_code; string method; string path; bytes body; }`
  - `ProxyToPeerResponse { int32 status_code; bytes body; }`
  - Resolves the peer's `base_url` from `peer_banks`, appends `path` (a leaf like `/negotiations/222/abc/accept` or `/public-stock`), signs (X-Api-Key + optional HMAC via `contract/sitxauth`), performs the HTTP call, returns status+body. This is the centralized egress the OTC domain (stock-service) will call at integration instead of dialing peers directly. Added to `contract/proto/transaction/transaction.proto`; `make proto`.

Listens on gRPC **:50062** (next free; verification is :50061).

### Ops HTTP surface (no business REST)
Separate `net/http` mux on **:9108**: `GET /metrics`, `GET /healthz` (always-200 once serving), `GET /readyz` (DB ping; report dependency dial state). Nothing else.

### Outbound dependencies (gRPC clients)
- **account-service** (`accountpb.AccountServiceClient`) — money legs (reserve/settle/release incoming+outgoing, get/list, update-balance). Required.
- **stock-service** (`stockpb.PeerOTCServiceClient`) — option legs (CheckSellerCanDeliver, ReserveSellerSharesForNewTx, ReleaseSellerSharesForNewTx, ValidatePeerOptionMoneyLeg, LookupPeerOptionContract, RecordOptionContract). Optional; degrades exactly as today when unreachable.
- **peer banks** (outbound HTTP via `PeerHTTPClient` + the new egress) — targets from `peer_banks`, signed via `contract/sitxauth`.

### Data
Owns `peer_banks`, `peer_idempotence_records`, `outbound_peer_txs` via `db.AutoMigrate`. Own env-configured Postgres DB (provisioned at integration; default port reserved 5443). No seed data. **No Kafka** (the engine uses none; parity kept).

### Config (env, `.env` walk-up)
`INTERBANK_GRPC_ADDR` (:50062), `INTERBANK_HTTP_ADDR` (:9108), `INTERBANK_DB_{HOST,PORT,USER,PASSWORD,NAME}`, `ACCOUNT_GRPC_ADDR`, `STOCK_GRPC_ADDR`, `OWN_BANK_CODE`, `SITX_RECEIVE_SYNC_DEADLINE`, `INTERBANK_PREPARE_TIMEOUT`, `INTERBANK_COMMIT_TIMEOUT`, `INTERBANK_RECEIVER_WAIT`, replay/reconcile intervals.

## Error handling
gRPC status codes mirror the ported handlers (InvalidArgument/FailedPrecondition/Internal/NotFound). 2PC semantics, idempotency-by-key, and forward-recovery (committing pivot) are preserved verbatim from the engine. Outbound HTTP maps transport failures to `ErrRetryLater`; the crons drive recovery. Ops `/readyz` returns 503 until DB reachable.

## Testing
Port the engine's unit tests (posting executor, vote builder, peer-http client, PeerTx handler incl. the GetTxStatus receiver-state cases, peer-bank admin, replay cron, reconciler) onto sqlite in-memory; add a `PeerEgressGRPCHandler` test (httptest peer, asserts method/path/signing/status passthrough). `go build ./...` + `go test ./...` + `golangci-lint run ./...` green for the new module. The existing transaction-service engine + tests stay untouched and green (copy, not move).

## Explicitly OUT of scope (later integration task)
- docker-compose / docker-compose-remote / Helm entries; the new DB provisioning.
- Re-pointing the gateway's `PeerTxServiceClient` / `PeerBankAdminServiceClient` to :50062.
- Routing stock-service's OTC/discovery egress through `PeerEgressService.ProxyToPeer`.
- Deleting the engine from transaction-service.
- These are captured as an "Integration checklist" in the plan.

## Versioning
Dormant service ⇒ no `/api/v3/version` behavior change; `VERSION` is **not** bumped by this task (also contended by a parallel agent). The proto gains an additive `PeerEgressService` (backward-compatible).
