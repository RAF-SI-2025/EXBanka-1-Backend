# interbank-service Implementation Plan

> Build the cross-bank SI-TX settlement engine as a **standalone, dormant, drop-in** gRPC service. Copy (don't move) from transaction-service; no deploy/gateway/Helm wiring yet.

**Goal:** A buildable, tested `interbank-service` implementing `transactionpb.PeerTxService` + `PeerBankAdminService` + a new `PeerEgressService`, gRPC-only (business) with ops-only HTTP (`/metrics`,`/healthz`,`/readyz`), centralizing all permitted outbound peer HTTP egress.

**Architecture / files:** see `docs/superpowers/specs/2026-06-06-interbank-service-extraction-design.md`.

---

## Build steps

1. **Scaffold** — `interbank-service/go.mod` (`module github.com/exbanka/interbank-service`, go 1.26.x), add `./interbank-service` to `go.work`. Create `cmd/` + `internal/{config,model,repository,sitx,handler,service,metrics,health}`.
2. **Port engine (copy + rewrite imports `github.com/exbanka/transaction-service/internal` → `github.com/exbanka/interbank-service/internal`):**
   - `internal/sitx/{posting_executor,peer_http_client,vote,vote_builder}.go` (+ tests)
   - `internal/handler/{peer_tx_grpc_handler,peer_bank_admin_grpc_handler}.go` (+ tests, incl. the GetTxStatus receiver-state fix already merged)
   - `internal/service/{outbound_replay_cron,peer_tx_reconciler}.go` (+ tests)
   - `internal/model/{peer_bank,peer_idempotence_record,outbound_peer_tx}.go`
   - `internal/repository/{peer_bank,peer_idempotence,outbound_peer_tx}_repository.go` (+ tests)
   - Shared infra reused as-is: `contract/cronreg`, `contract/shared/grpcmw`, `contract/sitx`, `contract/sitxauth`, `contract/{transaction,account,stock}pb`.
3. **Egress** — add `PeerEgressService.ProxyToPeer` to `contract/proto/transaction/transaction.proto`; `make proto`; implement `internal/handler/peer_egress_grpc_handler.go` (resolve peer base_url from peer_banks → append path → sign via sitxauth → HTTP → return {status,body}). Test with `httptest`.
4. **Service plumbing** — `internal/config/config.go` (env), `internal/metrics` (promhttp registry), `internal/health` (`/healthz` 200; `/readyz` DB ping), `cmd/main.go` (AutoMigrate the 3 tables; dial account [required] + stock [optional]; build executor+httpclient+handlers; start gRPC :50062 registering PeerTx+PeerBankAdmin+PeerEgress; start ops HTTP :9108; start replay + reconcile crons with ctx; graceful shutdown), `Dockerfile`.
5. **Verify** — `go mod tidy`; `go build ./...`; `go test ./...`; `golangci-lint run ./...`; confirm transaction-service unchanged + still green.

## Integration checklist (LATER — when deploy/Helm is possible; NOT in this task)

- [ ] Provision `interbank_db` (Postgres, port 5443) + Helm/k8s manifests + docker-compose / docker-compose-remote entries (gRPC 50062, http 9108, env, depends_on account+stock+db).
- [ ] api-gateway: point **every** `/cross-bank-protocol` gRPC client at `interbank-service:50062` — `PeerTxServiceClient`, `PeerBankAdminServiceClient`, `PeerEgressServiceClient`, the `PeerOTCServiceClient` used by `PeerOTCHandler` (was → stock-service), and switch `PeerUserHandler` from its direct client/user clients to interbank's `PeerUserServiceClient`. interbank-service is the single cross-bank backend; it forwards OTC → stock-service and /user → client/user-service. (stock-service still *serves* `PeerOTCService`, but now interbank — not the gateway — calls it.)
- [ ] api-gateway: expose the peer health surface — `GET /api/v3/peer-banks/{id}/reachability` → `PeerEgressService.CheckPeerReachability`, and `GET /api/v3/peer-banks/state` → `GetPeersState`; optionally have the `POST /api/v3/peer-banks` (create) handler chain a `CheckPeerReachability` and return the probe alongside the created row ("verify on add").
- [ ] stock-service: route outbound OTC/discovery egress (`peerotc.Client`, `otccache` fetches) through `PeerEgressService.ProxyToPeer` instead of dialing peers directly; drop its direct HTTP egress.
- [ ] transaction-service: delete the engine (the 14 files), drop the 3 tables from its AutoMigrate, remove PeerTx/PeerBankAdmin registration + crons; keep local payments/transfers/fees.
- [ ] Move the peer_banks data (or re-register peers) into interbank_db.
- [ ] Bump VERSION (the integration is the behavior-affecting change).

## Notes
- **VERSION not bumped** by this task (dormant; no served-behavior change; contended by a parallel agent).
- **No Kafka** (engine uses none).
- transaction-service's engine + tests remain (copy, not move) so nothing in the running system changes.
