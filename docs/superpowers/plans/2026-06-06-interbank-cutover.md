# Interbank-service Cutover Plan

> Goal: route the entire cross-bank protocol through `interbank-service`, replacing the cross-bank functions in stock-service (OTC egress) and transaction-service (TX engine).

## EXECUTION — clean cut (2026-06-07)

The flag-gated "make-ready-then-flip" strategy below was the original draft. The
actual execution is a **clean cut**, per the user's decision: no coexisting
old/new paths, no `*_unset ⇒ old behavior` fallback. Each service is re-pointed
at interbank-service unconditionally and the dead egress code is deleted in the
same change. Deploy ordering (not a flag) provides the safety: bring
interbank-service up with its `peer_banks` migrated **before** the gateway +
stock-service that now depend on it.

### Step 1 — gateway → interbank — ✅ DONE (commit `075b7a0c`)
- `api-gateway` config gained `INTERBANK_GRPC_ADDR` (default `interbank-service:50062`).
- `main.go` re-points **all** `/cross-bank-protocol` peer clients at interbank:
  `PeerTxServiceClient`, `PeerBankAdminServiceClient`, `PeerOTCServiceClient`,
  `PeerEgressServiceClient`, and a new `PeerUserServiceClient`.
- `PeerUserHandler` is now a thin forwarder over `PeerUserService.ResolvePeerUser`
  (found=false→404, else `{bankDisplayName, displayName}`); resolution semantics
  moved into interbank-service.
- Inbound PeerAuth resolves peers via interbank's `PeerBankAdminService.ResolvePeerByAPIToken`.

### Step 2 — stock-service egress → interbank — ✅ DONE (this commit)
- `stock-service` config: `TransactionGRPCAddr` → `InterbankGRPCAddr`
  (default `interbank-service:50062`). stock-service no longer dials
  transaction-service at all (its only conn was the cross-bank one).
- `main.go`: the single `interbankConn` backs `peerTxClient`
  (`InitiateOutboundTxWithPostings` for OTC settlement), `peerBankAdminClient`
  (`ListPeerBanks` for discovery), and a new `peerEgressClient`.
- New `internal/peeregress.Dispatcher` (implements `handler.PeerNegotiationDispatcher`)
  backed by `ProxyToPeer`: `CreateNegotiation` → `POST /negotiations`;
  `Proxy` → `{method} /negotiations/{rid}/{fid}{subpath}`. **`internal/peerotc`
  deleted.**
- `otccache` (`cache.go`, `option_cache.go`): `fetchPeer` now fetches
  `/public-stock` + `/public-option-offers` via `ProxyToPeer`; the per-cache
  `httpClient` + direct `ResolvePeerByBankCode`/`X-Api-Key` signing are gone.
  `ListPeerBanks` (via interbank) still enumerates which peers to poll.
- `docker-compose.yml`: stock-service `TRANSACTION_GRPC_ADDR` →
  `INTERBANK_GRPC_ADDR: interbank-service:50062`; `depends_on` transaction-service
  → interbank-service. (No cycle: interbank does **not** hard-depend on stock —
  its stock client is lazy/best-effort.)

### Step 3 — remove the SI-TX engine from transaction-service — ✅ DONE (this commit)
- Deleted 14 engine production files + 22 engine test files: the whole
  `internal/sitx/`, `peer_tx_grpc_handler.go`, `peer_bank_admin_grpc_handler.go`,
  the 3 peer repos, the 3 peer models, `outbound_replay_cron.go`,
  `peer_tx_reconciler.go`, and `handler/export_test.go` (peer-only accessor).
- `cmd/main.go`: removed the entire SI-TX wiring block (peer repos, executor,
  HTTP client, peerLookup, peerTxHandler, both crons), the stock-service conn +
  optionRecorder (engine-only), the `PeerBankAdminService`/`PeerTxService`
  registration, the 3 peer models from AutoMigrate, and the now-unused
  `sitx`/`stockpb`/`net/http`/`strconv` imports.
- `config.go`: removed all dead SI-TX config (StockGRPCAddr, the Inter-bank 2PC
  tuning block, InterbankReceiveSyncDeadline, OwnBankCode, per-peer
  endpoint/HMAC fields) + the now-unused `getDuration`/`getInt`/`time`/`strconv`;
  trimmed `config_test.go` accordingly. `go mod tidy` moved `uuid` + `crypto`
  (engine-only deps) to indirect.
- `docker-compose.yml`: dropped transaction-service's `STOCK_GRPC_ADDR`,
  `OWN_BANK_CODE`, `SITX_RECEIVE_SYNC_DEADLINE`, and the peer-egress
  `extra_hosts`. transaction-service → pure local payments/transfers/fees.
- Spec updated: §25 gRPC services + DB tables now attributed to interbank-service.
- **Deploy ordering (hard cutover, no rollback flag):** interbank-service must be
  up with `peer_banks` migrated/registered (interbank_db) before transaction-service
  is redeployed without the engine — otherwise inbound peer auth + outbound
  signing have no registry. Orphaned `peer_banks`/`peer_idempotence_records`/
  `outbound_peer_txs` rows remain in transaction_db (no longer migrated/read) and
  may be dropped manually post-migration.

**Cutover COMPLETE.** All three steps landed on Development; interbank-service is
the sole home of the cross-bank SI-TX engine + registry + egress.

---

## Original flag-gated draft (superseded by the clean cut above)

> Strategy: make everything ready behind a config switch, then flip it — both the
> old and new paths coexist so the cutover is a single env change + restart,
> instantly reversible, with no code redeploy at switch time.

### Phase 0 — DONE (committed)
- `interbank-service` built: serves the whole `/cross-bank-protocol` surface (PeerTx engine + PeerBankAdmin registry + PeerEgress egress/reachability/state; PeerOTC forwarder→stock; PeerUser resolver→client/user). gRPC `:50062`, ops HTTP `:9108`.
- CI (`ci.yml` build/test/lint/tidy) + CD (`cd.yml` build/publish) + `docker-compose.yml` (interbank-service + interbank-db `:5443`) include it.

### Phase 1 — MAKE READY (flag-gated; superseded — we re-pointed unconditionally instead)
- Gateway: `INTERBANK_GRPC_ADDR` set ⇒ dial all `/cross-bank-protocol` peer clients at interbank. (Executed clean, without the unset-fallback branch.)
- stock-service: `PeerEgressServiceClient` + a `ProxyToPeer`-backed `PeerNegotiationDispatcher`; `otccache` fetches via `ProxyToPeer`. (Executed clean, without the unset-fallback branch.)

### Phase 2 / 3 (flag flip + cleanup) — folded into the clean-cut steps above.

## Notes / risks
- **interbank↔stock cycle is fine** (lazy gRPC dials, no compile cycle): interbank forwards OTC to stock + calls stock for option legs; stock calls interbank for egress.
- **One inbound hop added** for OTC (gateway→interbank→stock) — negligible at cohort scale.
- **`peer_banks` data migration** is the only stateful step — do it before step 3 so inbound auth + outbound signing resolve peers.
- `VERSION` bumped on each step (2.16.x PATCH — internal re-wiring, no API contract change).
