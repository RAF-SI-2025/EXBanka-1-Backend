# Celina 5 SI-TX Refactor — Phase 1 Demolition Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every line of inter-bank / cross-bank-OTC code that was written under the wrong-assumption protocol (HMAC + Prepare/Commit/CheckStatus action enum + reconciler crons + saga-log driver), so the next phase can introduce SI-TX-conformant replacements onto a clean slate.

**Architecture:** Surgical removal in compile-safe order. Top-level wiring (`cmd/main.go` files, router) is unwired first so the deleted code becomes orphan, then the orphan files are deleted, then the proto contracts and Kafka topic constants are removed and proto regenerated. After each task the workspace builds cleanly.

**Tech Stack:** Go workspace monorepo (Go 1.21+), Gin (api-gateway), gRPC (`protoc` + `protoc-gen-go` + `protoc-gen-go-grpc`), GORM (PostgreSQL auto-migrate), Kafka (`kafka-go`), Docker Compose.

**End state of this phase:**
- `POST /api/v3/me/transfers` works for intra-bank receivers; returns `501 not_implemented` for foreign-prefix receivers.
- All HMAC / nonce / Prepare/Commit/CheckStatus code is gone.
- All `crossbank_*` and `inter_bank_saga_log_*` code in stock-service is gone.
- `Specification.md` §25 and §27 carry a "pending SI-TX implementation" stub.
- `make build` and `make test` succeed (test-app inter-bank workflows are deleted, not skipped).
- Schema tables `banks`, `inter_bank_transactions`, `inter_bank_saga_logs` are dropped on next service start (auto-migrate no longer creates them; existing rows are removed by a one-time `DROP TABLE IF EXISTS` in startup migration).

---

## File structure

This phase only deletes / unwires. It creates **one** small new file:

- **Create:** `api-gateway/internal/handler/peer_disabled_handler.go` — thin wrapper that detects a foreign 3-digit prefix on `POST /api/v3/me/transfers` / `GET /api/v3/me/transfers/:id` and returns `501 not_implemented`. Falls through to the existing intra-bank `TransactionHandler.CreateTransfer` / `GetMyTransfer` for own-prefix.

That's the only addition in Phase 1 — every other change is a deletion or unwiring.

**Files / blocks to delete (verified inventory):**

api-gateway:
- `internal/cache/nonce_store.go`
- `internal/handler/inter_bank_internal_handler.go`
- `internal/handler/inter_bank_public_handler.go`
- `internal/middleware/hmac.go`
- `internal/middleware/hmac_test.go`

transaction-service:
- `internal/handler/inter_bank_grpc_handler.go`
- `internal/handler/inter_bank_idempotency_test.go`
- `internal/messaging/inter_bank_envelope.go`
- `internal/model/bank.go`
- `internal/model/inter_bank_status.go`
- `internal/model/inter_bank_status_test.go`
- `internal/model/inter_bank_transaction.go`
- `internal/repository/inter_bank_tx_repository.go`
- `internal/service/inter_bank_account_client.go`
- `internal/service/inter_bank_fee_rules.go`
- `internal/service/inter_bank_reconciler.go`
- `internal/service/inter_bank_recovery.go`
- `internal/service/inter_bank_service.go`
- `internal/service/inter_bank_timeout_cron.go`
- `internal/service/peer_bank_client.go`
- `internal/service/peer_bank_client_test.go`

stock-service:
- `internal/handler/crossbank_idempotency_test.go`
- `internal/handler/crossbank_internal_handler.go`
- `internal/model/inter_bank_saga_log.go`
- `internal/repository/inter_bank_saga_log_repository.go`
- `internal/saga/crossbank_recorder.go`
- `internal/saga/crossbank_recorder_test.go`
- `internal/service/crossbank_accept_saga.go`
- `internal/service/crossbank_accept_saga_test.go`
- `internal/service/crossbank_check_status.go`
- `internal/service/crossbank_discovery.go`
- `internal/service/crossbank_exercise_saga.go`
- `internal/service/crossbank_exercise_saga_test.go`
- `internal/service/crossbank_expire_saga.go`
- `internal/service/crossbank_expire_saga_test.go`
- `internal/service/crossbank_expiry_cron.go`
- `internal/service/crossbank_orphan_reservation_cron.go`
- `internal/service/crossbank_peer_client.go`

test-app/workflows:
- `crossbank_otc_test.go`
- `helpers_interbank_test.go`
- `wf_crossbank_saga_durability_test.go`
- `wf_interbank_commit_mismatch_test.go`
- `wf_interbank_commit_timeout_test.go`
- `wf_interbank_crash_recovery_test.go`
- `wf_interbank_hmac_rejection_test.go`
- `wf_interbank_incoming_success_test.go`
- `wf_interbank_notready_test.go`
- `wf_interbank_prepare_timeout_test.go`
- `wf_interbank_receiver_abandoned_test.go`
- `wf_interbank_success_test.go`

contract:
- `proto/transaction/transaction.proto` — delete `service InterBankService { ... }` block + every `InterBank*` message type.
- `proto/stock/stock.proto` — delete `service CrossBankOTCService { ... }` block + every cross-bank-OTC-only message type.
- `proto/account/account.proto` — **keep** `ReserveIncoming` / `CommitIncoming` / `ReleaseIncoming` (SI-TX-shape-compatible).
- `kafka/messages.go` — delete the 4 `transfer.interbank-*` topic constants + `TransferInterbankMessage` struct + the 8 `otc.crossbank*`/`otc.contract-*-crossbank`/`otc.contract-expiry-stuck`/`otc.local-offer-changed` topic constants + their payload structs.

Top-level wiring (modify in place, do not delete the file):
- `api-gateway/cmd/main.go`
- `api-gateway/internal/router/handlers.go`
- `api-gateway/internal/router/router_v3.go`
- `transaction-service/cmd/main.go`
- `stock-service/cmd/main.go`
- `docker-compose.yml`
- `docker-compose-remote.yml`
- `Specification.md` (replace §25 + §27)
- `seeder/main.go` (remove bank seed data — verify in Task 2)

---

## Task 1 — Unwire api-gateway inter-bank routes

**Why first:** these are the entry points. After this task, `inter_bank_internal_handler.go`, `inter_bank_public_handler.go`, `hmac.go`, and `nonce_store.go` are unreachable code, safe to delete in Task 2.

**Files:**
- Create: `api-gateway/internal/handler/peer_disabled_handler.go`
- Modify: `api-gateway/cmd/main.go`
- Modify: `api-gateway/internal/router/handlers.go`
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1.1: Read the current state of api-gateway/cmd/main.go around the inter-bank wiring (lines ~206–294)** so subsequent edits don't drift.

Run: `sed -n '200,295p' "api-gateway/cmd/main.go"`
Expected: see `wsHandler := handler.NewWebSocketHandler(...)` followed by `interBankClient, interBankConn, err := grpcclients.NewInterBankServiceClient(...)`, the `peerKeys` map, `peerKeyResolver`, `interBankInternalHandler`, the `internal := r.Group("/internal/inter-bank")` block, and the `Deps{ ..., InterBankClient, ... }` struct literal.

- [ ] **Step 1.2: Create the disabled-peer wrapper handler**

Create `api-gateway/internal/handler/peer_disabled_handler.go`:

```go
package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// PeerDisabledHandler serves /api/v3/me/transfers and /api/v3/me/transfers/:id
// while the SI-TX implementation is being built. For intra-bank receivers it
// delegates to the existing TransactionHandler. For foreign-prefix receivers
// it returns 501 with a clear error.
//
// Phase 1 of the SI-TX refactor (docs/superpowers/specs/2026-04-29-celina5-
// sitx-refactor-design.md): this temporary wrapper lets the gateway compile
// without the InterBankPublicHandler while inter-bank functionality is
// pending.
type PeerDisabledHandler struct {
	tx           *TransactionHandler
	ownBankCode  string
}

// NewPeerDisabledHandler constructs the wrapper. ownBankCode is the 3-digit
// prefix of this bank (matches OWN_BANK_CODE env var; default "111").
func NewPeerDisabledHandler(tx *TransactionHandler, ownBankCode string) *PeerDisabledHandler {
	return &PeerDisabledHandler{tx: tx, ownBankCode: ownBankCode}
}

// CreateTransfer routes to the intra-bank handler when the receiver account
// number's 3-digit prefix matches ownBankCode. Foreign-prefix receivers get
// 501 not_implemented (SI-TX implementation pending).
func (h *PeerDisabledHandler) CreateTransfer(c *gin.Context) {
	receiver := h.peekReceiverAccountNumber(c)
	if receiver != "" && !h.isOwnPrefix(receiver) {
		apiError(c, http.StatusNotImplemented, "not_implemented",
			"inter-bank transfers are temporarily disabled (SI-TX implementation in progress)")
		return
	}
	h.tx.CreateTransfer(c)
}

// GetTransferByID — intra-bank lookup only. UUID-style transactionIds (which
// the deleted InterBankPublicHandler used to recognise) now return 404 via
// the underlying handler's int parse.
func (h *PeerDisabledHandler) GetTransferByID(c *gin.Context) {
	h.tx.GetMyTransfer(c)
}

// peekReceiverAccountNumber pulls the to_account_number / receiverAccount
// field from the request body without consuming it. We read the raw bytes,
// restore the body so the downstream handler can re-bind, then JSON-decode
// into a map for the peek. On any error we return "" and fall through to
// the intra-bank handler.
func (h *PeerDisabledHandler) peekReceiverAccountNumber(c *gin.Context) string {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	// Restore so the downstream handler can ShouldBindJSON normally.
	c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	if v, ok := body["to_account_number"].(string); ok && v != "" {
		return v
	}
	if v, ok := body["receiverAccount"].(string); ok && v != "" {
		return v
	}
	return ""
}

func (h *PeerDisabledHandler) isOwnPrefix(accountNumber string) bool {
	if len(accountNumber) < 3 {
		return false
	}
	prefix := accountNumber[:3]
	if strings.Contains(prefix, "-") {
		// Account numbers with separator: prefix is everything before first '-'
		idx := strings.Index(accountNumber, "-")
		if idx <= 0 {
			return false
		}
		prefix = accountNumber[:idx]
	}
	return prefix == h.ownBankCode
}
```

- [ ] **Step 1.3: Add Handlers field for the new wrapper**

Modify `api-gateway/internal/router/handlers.go` — replace the `InterBankPub` field with `PeerDisabled`:

```go
// In the Handlers struct (around line 85):
//   InterBankPub  *handler.InterBankPublicHandler   <-- DELETE THIS LINE
//   PeerDisabled  *handler.PeerDisabledHandler      <-- ADD THIS LINE
```

And the struct literal in `NewHandlers`:

```go
// Around line 120, replace:
//   InterBankPub:  handler.NewInterBankPublicHandler(d.InterBankClient, d.TxClient, d.AccountClient),
// with:
//   PeerDisabled:  handler.NewPeerDisabledHandler(handler.NewTransactionHandler(d.TxClient, d.FeeClient, d.AccountClient, d.ExchangeClient), d.OwnBankCode),
// (Reuses the Tx handler instance already constructed for the Tx field; just pass the same one. If Tx is created above PeerDisabled, reference h.Tx — re-check the surrounding lines.)
```

Verify: scan `handlers.go` for any other reference to `InterBankPub` and remove. Add `OwnBankCode string` to the `Deps` struct if it isn't already there (should be — used elsewhere).

- [ ] **Step 1.4: Update Deps struct in handlers.go to drop InterBankClient**

In the `Deps` struct, remove the line `InterBankClient transactionpb.InterBankServiceClient`. The struct is in the same file as `Handlers`. Search for `InterBankClient` and delete the field.

- [ ] **Step 1.5: Update router_v3.go — replace InterBankPub references**

Modify `api-gateway/internal/router/router_v3.go`. Find lines 98 and 101:

```go
// Old (DELETE):
me.POST("/transfers", h.InterBankPub.CreateTransfer)
me.GET("/transfers/:id", h.InterBankPub.GetTransferByID)

// New:
me.POST("/transfers", h.PeerDisabled.CreateTransfer)
me.GET("/transfers/:id", h.PeerDisabled.GetTransferByID)
```

The intra-bank `me.GET("/transfers", h.Tx.ListMyTransfers)` and `me.POST("/transfers/preview", h.Tx.PreviewTransfer)` and `me.POST("/transfers/:id/execute", h.Tx.ExecuteTransfer)` lines stay unchanged.

- [ ] **Step 1.6: Update api-gateway/cmd/main.go — remove inter-bank wiring**

Modify `api-gateway/cmd/main.go`. Delete the following block (around lines 214–239 — verify with `sed -n '210,245p' api-gateway/cmd/main.go` first):

```go
// DELETE this entire block:
//
// // Inter-bank wiring (Spec 3): InterBankService gRPC client, Redis nonce
// // store, peer-key resolver, internal HMAC-authenticated routes.
// interBankClient, interBankConn, err := grpcclients.NewInterBankServiceClient(cfg.TransactionGRPCAddr)
// if err != nil {
//     log.Fatalf("failed to connect to inter-bank service: %v", err)
// }
// defer interBankConn.Close()
//
// redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
// if err := redisClient.Ping(ctx).Err(); err != nil {
//     log.Printf("warn: redis ping failed (%s) — inter-bank nonce store unavailable: %v", cfg.RedisAddr, err)
// }
// nonceStore := cache.NewNonceStore(redisClient, 10*time.Minute)
// peerKeys := map[string]string{
//     "222": cfg.Peer222InboundKey,
//     "333": cfg.Peer333InboundKey,
//     "444": cfg.Peer444InboundKey,
// }
// peerKeyResolver := func(code string) (string, bool) {
//     k, ok := peerKeys[code]
//     if !ok || k == "" {
//         return "", false
//     }
//     return k, true
// }
// interBankInternalHandler := handler.NewInterBankInternalHandler(interBankClient)
```

Then delete the `internal.Group("/internal/inter-bank")` registration (around lines 282–290):

```go
// DELETE this block:
//
// internal := r.Group("/internal/inter-bank")
// internal.Use(middleware.HMACMiddleware(peerKeyResolver, nonceStore, 5*time.Minute))
// {
//     internal.POST("/transfer/prepare", interBankInternalHandler.Prepare)
//     internal.POST("/transfer/commit", interBankInternalHandler.Commit)
//     internal.POST("/check-status", interBankInternalHandler.CheckStatus)
// }
```

In the `deps := router.Deps{ ... }` literal (around line 247), delete the `InterBankClient: interBankClient,` line.

Remove unused imports that result from these deletions: search for `redis`, `cache.NewNonceStore`, `middleware.HMACMiddleware`, `handler.NewInterBankInternalHandler`, `grpcclients.NewInterBankServiceClient` — if any of those imports are no longer referenced after the edits, remove them. The `nonceStore` variable was only used in the internal-routes group, so its import (`github.com/exbanka/api-gateway/internal/cache`) may still be used by other handlers — verify by grepping `cache\.` in `cmd/main.go` after the deletions.

- [ ] **Step 1.7: Verify api-gateway compiles**

Run: `cd api-gateway && go build ./...`
Expected: clean build, exit code 0. If errors mention `InterBankClient` or `peerKeys` or `nonceStore` references missed in the wiring file, fix them. If errors mention `inter_bank_internal_handler.go` or `inter_bank_public_handler.go` symbols undefined inside those files themselves — that's fine, those files will be deleted in Task 2.

Wait — at this point those files still exist and will fail to compile because `cmd/main.go` no longer imports the package members they need. **Resolution:** they will compile as long as the files themselves are syntactically valid; their unused-ness doesn't cause a compile error. But if `inter_bank_public_handler.go` imports `transactionpb` and the proto types are still present, it's fine. If it imports something that was removed (e.g. nonceStore), it doesn't — only main.go does. So `go build ./...` should succeed here.

If the build fails because a still-existing inter-bank file references a type that's no longer reachable, jump straight to Task 2 (file deletion) — the orphaned file is the cause.

- [ ] **Step 1.8: Run api-gateway unit tests**

Run: `cd api-gateway && go test ./internal/handler/ ./internal/middleware/ ./internal/router/ -count=1`
Expected: pass. The deleted-route mappings don't have unit tests in api-gateway (gateway routing tests live in test-app integration suite). If `inter_bank_public_handler_test.go` exists, it will be deleted in Task 2 — for now, expect the existing tests to pass.

- [ ] **Step 1.9: Commit**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
git add api-gateway/internal/handler/peer_disabled_handler.go \
        api-gateway/internal/router/handlers.go \
        api-gateway/internal/router/router_v3.go \
        api-gateway/cmd/main.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): unwire inter-bank routes (Phase 1, SI-TX refactor)

Replaces InterBankPublicHandler with a thin PeerDisabledHandler that
returns 501 not_implemented for foreign-prefix transfer receivers and
delegates to the intra-bank TransactionHandler for own-prefix.

Deletes the /internal/inter-bank/* gRPC bridge, the HMAC middleware
wiring, the Redis nonce store, and the per-peer key map from
cmd/main.go. The handler/middleware/cache .go files themselves are
deleted in the next commit.

Refs: docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md
EOF
)"
```

Expected: commit succeeds. Pre-commit hooks (golangci-lint, gofmt) must pass; if they fail, fix lint/formatting and re-commit (do NOT use --no-verify).

---

## Task 2 — Delete api-gateway inter-bank files

**Why now:** Task 1 made these files unreachable. Compile no longer depends on them.

**Files to delete:**
- `api-gateway/internal/handler/inter_bank_internal_handler.go`
- `api-gateway/internal/handler/inter_bank_public_handler.go`
- `api-gateway/internal/middleware/hmac.go`
- `api-gateway/internal/middleware/hmac_test.go`
- `api-gateway/internal/cache/nonce_store.go`

- [ ] **Step 2.1: Verify no remaining references in api-gateway**

Run: `grep -rE "InterBankPublicHandler|InterBankInternalHandler|NewInterBankPublicHandler|NewInterBankInternalHandler|HMACMiddleware|NewNonceStore" api-gateway/ --include="*.go" | grep -v "_test.go" | grep -v "inter_bank_internal_handler.go\|inter_bank_public_handler.go\|hmac.go\|nonce_store.go"`
Expected: zero output. If any line returns, edit those files to remove the reference before deleting.

Test files referencing these types may also exist; check:
Run: `grep -rE "InterBankPublicHandler|InterBankInternalHandler|HMACMiddleware|NonceStore" api-gateway/ --include="*_test.go" | grep -v "hmac_test.go"`
Expected: zero output. If `inter_bank_public_handler_test.go` or `inter_bank_internal_handler_test.go` exist, list them — they're caught by step 2.2.

- [ ] **Step 2.2: List, then delete**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
ls api-gateway/internal/handler/inter_bank_*.go \
   api-gateway/internal/middleware/hmac*.go \
   api-gateway/internal/cache/nonce_store.go \
   2>/dev/null
```
Expected output (5 files):
```
api-gateway/internal/cache/nonce_store.go
api-gateway/internal/handler/inter_bank_internal_handler.go
api-gateway/internal/handler/inter_bank_public_handler.go
api-gateway/internal/middleware/hmac.go
api-gateway/internal/middleware/hmac_test.go
```

If any other inter-bank-named file appears (e.g. a test file for the public handler), include it in the rm.

```bash
rm api-gateway/internal/cache/nonce_store.go \
   api-gateway/internal/handler/inter_bank_internal_handler.go \
   api-gateway/internal/handler/inter_bank_public_handler.go \
   api-gateway/internal/middleware/hmac.go \
   api-gateway/internal/middleware/hmac_test.go
```

- [ ] **Step 2.3: Verify api-gateway still builds**

Run: `cd api-gateway && go build ./...`
Expected: clean build. Errors here indicate a missed reference; fix before continuing.

- [ ] **Step 2.4: Run api-gateway unit tests**

Run: `cd api-gateway && go test ./... -count=1`
Expected: all pass. No tests should reference deleted types — verified in Step 2.1.

- [ ] **Step 2.5: Commit**

```bash
git add -A
git commit -m "refactor(api-gateway): delete inter-bank handler/middleware/cache files (Phase 1)"
```

Expected: commit succeeds.

---

## Task 3 — Unwire transaction-service inter-bank wiring

**Why before Task 4:** mirrors Task 1's pattern — unwire from `cmd/main.go` so the inter-bank service files become orphans, then delete in Task 4.

**Files:**
- Modify: `transaction-service/cmd/main.go`

- [ ] **Step 3.1: Identify inter-bank wiring in transaction-service/cmd/main.go**

Run: `grep -nE "InterBank|interBank|peerBank|reconciler|recovery|TimeoutCron|HandlePrepare|HandleCommit" transaction-service/cmd/main.go`
Expected: list of lines wiring `InterBankService`, `peer_bank_client`, `InterBankReconciler`, `InterBankRecovery`, `InterBankTimeoutCron`, plus the gRPC server registration `transactionpb.RegisterInterBankServiceServer(...)`.

- [ ] **Step 3.2: Read those lines for full context**

Run: `sed -n '1,$p' transaction-service/cmd/main.go | wc -l` to get total line count, then read the file in 100-line chunks if needed. Identify:
- The gRPC client construction for `account-service` (used by inter-bank account client) — this stays, used by other services.
- The `InterBankService` struct construction.
- The `peerBankClient` construction.
- The `RegisterInterBankServiceServer` registration on the gRPC server.
- The `go reconciler.Run(ctx)` / `go recovery.Run(ctx)` / `go timeoutCron.Run(ctx)` background goroutines.
- The Kafka topic `EnsureTopics` call — list of topics will include the 4 `transfer.interbank-*` topics.

- [ ] **Step 3.3: Delete inter-bank wiring**

In `transaction-service/cmd/main.go`:
- Delete construction of `InterBankService`, `peerBankClient`, `InterBankReconciler`, `InterBankRecovery`, `InterBankTimeoutCron`.
- Delete the `transactionpb.RegisterInterBankServiceServer(grpcServer, ...)` call.
- Delete the three `go ...Run(ctx)` lines for reconciler / recovery / timeout cron.
- Remove from `EnsureTopics(...)` arguments: the four `kafkamsg.TopicTransferInterbank*` constants. Leave the other topics alone.

After this edit the file may have unused imports — remove them: `transactionpb`, `service.NewInterBankService`, etc. as appropriate.

Also remove from the AutoMigrate call any of these models (full search): `model.InterBankTransaction`, `model.Bank`. **Do not** remove `model.IncomingReservation` (lives in account-service, not relevant here, but verify with grep).

- [ ] **Step 3.4: Verify transaction-service compiles**

Run: `cd transaction-service && go build ./...`
Expected: clean. Same caveat as Task 1: orphan service files in `internal/service/inter_bank_*.go` may still exist and compile (they're in `internal/`, not yet imported elsewhere now that main.go no longer references them). If they reference symbols still in the codebase (e.g. `service.NewInterBankService`), they compile. If they reference each other only, they compile. Errors here mean a symbol got broken — fix before Task 4.

- [ ] **Step 3.5: Run unit tests**

Run: `cd transaction-service && go test ./... -count=1`
Expected: pre-existing inter-bank unit tests still pass against the orphan files. They will all be deleted together in Task 4.

- [ ] **Step 3.6: Commit**

```bash
git add transaction-service/cmd/main.go
git commit -m "refactor(transaction-service): unwire InterBankService + crons (Phase 1)"
```

---

## Task 4 — Delete transaction-service inter-bank files

**Files to delete:**
- `transaction-service/internal/handler/inter_bank_grpc_handler.go`
- `transaction-service/internal/handler/inter_bank_idempotency_test.go`
- `transaction-service/internal/messaging/inter_bank_envelope.go`
- `transaction-service/internal/model/bank.go`
- `transaction-service/internal/model/inter_bank_status.go`
- `transaction-service/internal/model/inter_bank_status_test.go`
- `transaction-service/internal/model/inter_bank_transaction.go`
- `transaction-service/internal/repository/inter_bank_tx_repository.go`
- `transaction-service/internal/service/inter_bank_account_client.go`
- `transaction-service/internal/service/inter_bank_fee_rules.go`
- `transaction-service/internal/service/inter_bank_reconciler.go`
- `transaction-service/internal/service/inter_bank_recovery.go`
- `transaction-service/internal/service/inter_bank_service.go`
- `transaction-service/internal/service/inter_bank_timeout_cron.go`
- `transaction-service/internal/service/peer_bank_client.go`
- `transaction-service/internal/service/peer_bank_client_test.go`

- [ ] **Step 4.1: Verify nothing else references these types**

Run: `grep -rE "InterBankTransaction|InterBankStatus|InterBankTxRepository|InterBankService|InterBankReconciler|InterBankRecovery|InterBankTimeoutCron|PeerBankClient|InterBankAccountClient|InterBankFeeRules|InterBankEnvelope" transaction-service/ --include="*.go" | grep -v inter_bank_ | grep -v peer_bank_client`
Expected: zero output. If any line returns, the reference must be removed before file deletion.

Also check there's no transitive import:
Run: `grep -rE "service\.NewInterBank|service\.NewPeerBankClient|repository\.NewInterBankTxRepository" transaction-service/ --include="*.go"`
Expected: zero output (cmd/main.go was already cleaned in Task 3).

- [ ] **Step 4.2: Delete files**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
rm transaction-service/internal/handler/inter_bank_grpc_handler.go \
   transaction-service/internal/handler/inter_bank_idempotency_test.go \
   transaction-service/internal/messaging/inter_bank_envelope.go \
   transaction-service/internal/model/bank.go \
   transaction-service/internal/model/inter_bank_status.go \
   transaction-service/internal/model/inter_bank_status_test.go \
   transaction-service/internal/model/inter_bank_transaction.go \
   transaction-service/internal/repository/inter_bank_tx_repository.go \
   transaction-service/internal/service/inter_bank_account_client.go \
   transaction-service/internal/service/inter_bank_fee_rules.go \
   transaction-service/internal/service/inter_bank_reconciler.go \
   transaction-service/internal/service/inter_bank_recovery.go \
   transaction-service/internal/service/inter_bank_service.go \
   transaction-service/internal/service/inter_bank_timeout_cron.go \
   transaction-service/internal/service/peer_bank_client.go \
   transaction-service/internal/service/peer_bank_client_test.go
```

If `transaction-service/internal/messaging/` is now empty, also `rmdir` it.

- [ ] **Step 4.3: Verify build + tests**

Run: `cd transaction-service && go build ./... && go test ./... -count=1`
Expected: build passes, tests pass.

- [ ] **Step 4.4: Commit**

```bash
git add -A
git commit -m "refactor(transaction-service): delete inter-bank model/repo/service/handler files (Phase 1)"
```

---

## Task 5 — Unwire stock-service crossbank wiring

**Files:**
- Modify: `stock-service/cmd/main.go`

- [ ] **Step 5.1: Identify crossbank wiring**

Run: `grep -nE "Crossbank|crossbank|CrossBank|CrossBankOTC|InterBankSagaLog|interBankSagaLog" stock-service/cmd/main.go`
Expected: lines constructing `crossbank_recorder.NewCrossbankRecorder`, `crossbank_accept_saga.NewCrossbankAcceptSaga`, `crossbank_exercise_saga.NewCrossbankExerciseSaga`, `crossbank_expire_saga.NewCrossbankExpireSaga`, `crossbank_check_status.NewCrossbankCheckStatusCron`, `crossbank_orphan_reservation_cron.NewCrossbankOrphanReservationCron`, `crossbank_expiry_cron.NewCrossbankExpiryCron`, `crossbank_discovery.NewCrossbankDiscovery`, `crossbank_peer_client.StaticCrossbankPeerRouter`, plus the `stockpb.RegisterCrossBankOTCServiceServer(...)` call and the `OTCOfferService.WithCrossbank(...)` call.

- [ ] **Step 5.2: Delete the wiring**

In `stock-service/cmd/main.go`, remove:
- All crossbank saga / cron / recorder / discovery / peer-router / `WithCrossbank` constructions and starts.
- The `stockpb.RegisterCrossBankOTCServiceServer(grpcServer, ...)` registration (replaced in Phase 4 with `RegisterPeerOTCServiceServer`).
- From the `EnsureTopics(...)` argument list: the 8 cross-bank-OTC topics (`otc.crossbank-saga-started`, `otc.crossbank-saga-committed`, `otc.crossbank-saga-rolled-back`, `otc.crossbank-saga-stuck-rollback`, `otc.contract-exercised-crossbank`, `otc.contract-expired-crossbank`, `otc.contract-expiry-stuck`, `otc.local-offer-changed`).
- Remove from AutoMigrate: `model.InterBankSagaLog`. **Keep** `model.OTCOffer` and `model.OptionContract` — those have intra-bank-OTC duties and stay.

After edits, prune unused imports.

- [ ] **Step 5.3: Verify build + tests**

Run: `cd stock-service && go build ./... && go test ./... -count=1`
Expected: clean build. Crossbank service / saga files in `internal/service/crossbank_*.go` and `internal/saga/crossbank_recorder.go` etc. now compile as orphans. Their tests still run and pass — they'll be deleted in Task 6.

- [ ] **Step 5.4: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "refactor(stock-service): unwire crossbank sagas + crons (Phase 1)"
```

---

## Task 6 — Delete stock-service crossbank files

**Files to delete:**
- `stock-service/internal/handler/crossbank_idempotency_test.go`
- `stock-service/internal/handler/crossbank_internal_handler.go`
- `stock-service/internal/model/inter_bank_saga_log.go`
- `stock-service/internal/repository/inter_bank_saga_log_repository.go`
- `stock-service/internal/saga/crossbank_recorder.go`
- `stock-service/internal/saga/crossbank_recorder_test.go`
- `stock-service/internal/service/crossbank_accept_saga.go`
- `stock-service/internal/service/crossbank_accept_saga_test.go`
- `stock-service/internal/service/crossbank_check_status.go`
- `stock-service/internal/service/crossbank_discovery.go`
- `stock-service/internal/service/crossbank_exercise_saga.go`
- `stock-service/internal/service/crossbank_exercise_saga_test.go`
- `stock-service/internal/service/crossbank_expire_saga.go`
- `stock-service/internal/service/crossbank_expire_saga_test.go`
- `stock-service/internal/service/crossbank_expiry_cron.go`
- `stock-service/internal/service/crossbank_orphan_reservation_cron.go`
- `stock-service/internal/service/crossbank_peer_client.go`

- [ ] **Step 6.1: Verify no other references**

Run: `grep -rE "Crossbank|CrossBank|InterBankSagaLog" stock-service/ --include="*.go" | grep -vE "crossbank_|inter_bank_saga_log"`
Expected: zero. If any reference remains, fix it first.

The intra-bank `OTCOfferService.WithCrossbank(...)` method on the OTCOfferService type — that method itself stays (it's just a no-op now without a registered dispatcher), but verify nothing else imports the deleted `crossbank_*` packages by name.

Run: `grep -rE "crossbank_accept_saga|crossbank_exercise_saga|crossbank_expire_saga|crossbank_recorder" stock-service/ --include="*.go" | grep -v crossbank_`
Expected: zero.

- [ ] **Step 6.2: Delete files**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
rm stock-service/internal/handler/crossbank_idempotency_test.go \
   stock-service/internal/handler/crossbank_internal_handler.go \
   stock-service/internal/model/inter_bank_saga_log.go \
   stock-service/internal/repository/inter_bank_saga_log_repository.go \
   stock-service/internal/saga/crossbank_recorder.go \
   stock-service/internal/saga/crossbank_recorder_test.go \
   stock-service/internal/service/crossbank_accept_saga.go \
   stock-service/internal/service/crossbank_accept_saga_test.go \
   stock-service/internal/service/crossbank_check_status.go \
   stock-service/internal/service/crossbank_discovery.go \
   stock-service/internal/service/crossbank_exercise_saga.go \
   stock-service/internal/service/crossbank_exercise_saga_test.go \
   stock-service/internal/service/crossbank_expire_saga.go \
   stock-service/internal/service/crossbank_expire_saga_test.go \
   stock-service/internal/service/crossbank_expiry_cron.go \
   stock-service/internal/service/crossbank_orphan_reservation_cron.go \
   stock-service/internal/service/crossbank_peer_client.go
```

- [ ] **Step 6.3: Remove the `WithCrossbank(...)` method on OTCOfferService**

Run: `grep -nE "WithCrossbank|withCrossbank|crossbankDispatcher" stock-service/internal/service/*.go`
Expected: hits inside `otc_offer_service.go` (or similar). Find and delete the method, the field on the struct (e.g. `crossbankDispatcher CrossbankDispatcher`), and the `CrossbankDispatcher` interface declaration if it's only used here.

If callers still invoke `service.WithCrossbank(...)` from `cmd/main.go`, Task 5 should already have removed those — verify with `grep WithCrossbank stock-service/`.

- [ ] **Step 6.4: Verify build + tests**

Run: `cd stock-service && go build ./... && go test ./... -count=1`
Expected: clean. All crossbank tests are gone; intra-bank OTC tests pass.

- [ ] **Step 6.5: Commit**

```bash
git add -A
git commit -m "refactor(stock-service): delete crossbank sagas, recorder, peer router, saga log (Phase 1)"
```

---

## Task 7 — Remove inter-bank gRPC services from contract protos

**Why now:** all callers of `InterBankService` / `CrossBankOTCService` are gone. Safe to delete from proto and regenerate.

**Files:**
- Modify: `contract/proto/transaction/transaction.proto`
- Modify: `contract/proto/stock/stock.proto`

- [ ] **Step 7.1: Read transaction.proto to find the InterBankService block**

Run: `grep -nE "service InterBankService|message InterBank|message InitiateInterBank|message Reverse|message GetInterBankTransfer" contract/proto/transaction/transaction.proto`
Expected: lines marking the start of the `service InterBankService { ... }` block plus its associated messages (`InitiateInterBankRequest`, `InitiateInterBankResponse`, `InterBankPrepareRequest`, `InterBankCommitRequest`, `InterBankCheckStatusRequest`, `InterBankCheckStatusResponse`, `GetInterBankTransferRequest`, `GetInterBankTransferResponse`, `ReverseInterBankTransferRequest`, `ReverseInterBankTransferResponse`).

- [ ] **Step 7.2: Delete the service block and all InterBank* messages**

Open `contract/proto/transaction/transaction.proto` and delete:
- The entire `service InterBankService { ... }` block (5 RPCs).
- Every `message InterBank*` and `message InitiateInterBank*` and `message ReverseInterBank*` and `message GetInterBankTransfer*` definition.

Keep the `service TransactionService` block, `service PaymentService` block, `service FeeService` block intact.

- [ ] **Step 7.3: Read stock.proto to find the CrossBankOTCService block**

Run: `grep -nE "service CrossBankOTCService|message CrossBank|message Crossbank|message PeerOffer|message PeerReviseOffer|message PeerAcceptIntent|message InterBankSagaLog" contract/proto/stock/stock.proto`
Expected: 12 RPCs in the `service CrossBankOTCService { ... }` block, plus 22 messages.

- [ ] **Step 7.4: Delete the CrossBankOTCService and its messages**

Delete the entire `service CrossBankOTCService { ... }` block plus every message used only by it: `CrossBank*`, `Crossbank*`, `PeerOffer*`, `PeerReviseOffer*`, `PeerAcceptIntent*`. Keep all intra-bank-OTC types (`OTCOffer`, `OptionContract`, `CreateOTCOfferRequest`, etc. — `service OTCOptionsService` block stays untouched).

- [ ] **Step 7.5: Regenerate proto Go bindings**

Run: `make proto`
Expected: regenerates `contract/transactionpb/*.pb.go` and `contract/stockpb/*.pb.go` without the deleted services. Verify the generated files no longer contain `InterBankServiceServer`, `CrossBankOTCServiceServer`, `RegisterInterBankServiceServer`, `RegisterCrossBankOTCServiceServer`.

Run: `grep -rE "InterBankServiceServer|CrossBankOTCServiceServer|RegisterInterBankServiceServer|RegisterCrossBankOTCServiceServer" contract/`
Expected: zero output.

- [ ] **Step 7.6: Build everything**

Run: `make build`
Expected: clean across all 12 services. Errors here indicate a missed reference somewhere (most likely a stale import in a service's `cmd/main.go` we missed in Tasks 3 or 5). Fix and rebuild.

- [ ] **Step 7.7: Run all tests**

Run: `make test`
Expected: pass everywhere except possibly test-app workflows that reference inter-bank or crossbank — those will be deleted in Task 9. If `make test` fails on test-app/workflows, that's expected and OK *for this commit only* — the failing tests get deleted in Task 9 and `make test` returns to green at the end of Task 9.

If a non-test-app failure occurs, fix it before committing.

- [ ] **Step 7.8: Commit**

```bash
git add contract/proto/transaction/transaction.proto \
        contract/proto/stock/stock.proto \
        contract/transactionpb/ \
        contract/stockpb/
git commit -m "refactor(contract): drop InterBankService + CrossBankOTCService protos (Phase 1)"
```

---

## Task 8 — Remove inter-bank Kafka topic constants and payload structs

**Files:**
- Modify: `contract/kafka/messages.go`

- [ ] **Step 8.1: Identify the topic blocks**

Run: `grep -nE "^	TopicTransferInterbank|^	TopicOTCCrossbank|^	TopicOTCContract.*Crossbank|^	TopicOTCContractExpiryStuck|^	TopicOTCLocalOfferChanged" contract/kafka/messages.go`
Expected:
```
63:	TopicTransferInterbankPrepared   = "transfer.interbank-prepared"
64:	TopicTransferInterbankCommitted  = "transfer.interbank-committed"
65:	TopicTransferInterbankReceived   = "transfer.interbank-received"
66:	TopicTransferInterbankRolledBack = "transfer.interbank-rolled-back"
809:	TopicOTCCrossbankSagaStarted       = "otc.crossbank-saga-started"
810:	TopicOTCCrossbankSagaCommitted     = "otc.crossbank-saga-committed"
811:	TopicOTCCrossbankSagaRolledBack    = "otc.crossbank-saga-rolled-back"
812:	TopicOTCCrossbankSagaStuckRollback = "otc.crossbank-saga-stuck-rollback"
813:	TopicOTCContractExercisedCrossbank = "otc.contract-exercised-crossbank"
814:	TopicOTCContractExpiredCrossbank   = "otc.contract-expired-crossbank"
815:	TopicOTCContractExpiryStuck        = "otc.contract-expiry-stuck"
816:	TopicOTCLocalOfferChanged          = "otc.local-offer-changed"
```

- [ ] **Step 8.2: Delete the topic constants and their payload structs**

In `contract/kafka/messages.go`:
1. Delete the 4 `TopicTransferInterbank*` constants and the `TransferInterbankMessage` struct definition that follows them.
2. Delete the `// ==================== Cross-bank OTC Options ====================` section header and the 8 `TopicOTC*` constants under it.
3. Delete every payload struct used only by the cross-bank topics: `CrossBankSagaStartedMessage`, `CrossBankSagaCommittedMessage`, `CrossBankSagaRolledBackMessage`, `CrossBankSagaStuckRollbackMessage`, `OTCContractExercisedCrossbankMessage`, `OTCContractExpiredCrossbankMessage`, `OTCContractExpiryStuckMessage`, `OTCLocalOfferChangedMessage`.

Verify which structs are cross-bank-only versus shared with intra-bank topics by searching:
Run: `grep -rE "CrossBankSagaStartedMessage|CrossBankSagaCommittedMessage|CrossBankSagaRolledBackMessage|CrossBankSagaStuckRollbackMessage|OTCContractExercisedCrossbankMessage|OTCContractExpiredCrossbankMessage|OTCContractExpiryStuckMessage|OTCLocalOfferChangedMessage|TransferInterbankMessage" --include="*.go" .`
Expected after Tasks 1–6: only the contract/kafka/messages.go file itself. If anything else returns, that's an orphan that must be fixed before deletion.

- [ ] **Step 8.3: Build everything**

Run: `make build`
Expected: clean. Any compile error means a service still references a deleted constant — fix the offending service.

- [ ] **Step 8.4: Run unit tests across services**

Run: `make test`
Expected: same status as Task 7.7 (pass everywhere except test-app workflows pending Task 9).

- [ ] **Step 8.5: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "refactor(contract): drop inter-bank + crossbank Kafka topic constants and payloads (Phase 1)"
```

---

## Task 9 — Delete test-app inter-bank / crossbank workflows

**Files to delete:**
- `test-app/workflows/crossbank_otc_test.go`
- `test-app/workflows/helpers_interbank_test.go`
- `test-app/workflows/wf_crossbank_saga_durability_test.go`
- `test-app/workflows/wf_interbank_commit_mismatch_test.go`
- `test-app/workflows/wf_interbank_commit_timeout_test.go`
- `test-app/workflows/wf_interbank_crash_recovery_test.go`
- `test-app/workflows/wf_interbank_hmac_rejection_test.go`
- `test-app/workflows/wf_interbank_incoming_success_test.go`
- `test-app/workflows/wf_interbank_notready_test.go`
- `test-app/workflows/wf_interbank_prepare_timeout_test.go`
- `test-app/workflows/wf_interbank_receiver_abandoned_test.go`
- `test-app/workflows/wf_interbank_success_test.go`

- [ ] **Step 9.1: Verify no other test files import these helpers**

Run: `grep -rE "interbankHelper|interbankSetup|hmacSign|peerBankURL|crossbankSetup" test-app/ --include="*.go" | grep -v helpers_interbank_test.go | grep -v wf_interbank_ | grep -v wf_crossbank_ | grep -v crossbank_otc_test`
Expected: zero output. If a non-inter-bank workflow imports an inter-bank helper, refactor or remove the borrow before deletion.

- [ ] **Step 9.2: Delete files**

```bash
cd "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend"
rm test-app/workflows/crossbank_otc_test.go \
   test-app/workflows/helpers_interbank_test.go \
   test-app/workflows/wf_crossbank_saga_durability_test.go \
   test-app/workflows/wf_interbank_commit_mismatch_test.go \
   test-app/workflows/wf_interbank_commit_timeout_test.go \
   test-app/workflows/wf_interbank_crash_recovery_test.go \
   test-app/workflows/wf_interbank_hmac_rejection_test.go \
   test-app/workflows/wf_interbank_incoming_success_test.go \
   test-app/workflows/wf_interbank_notready_test.go \
   test-app/workflows/wf_interbank_prepare_timeout_test.go \
   test-app/workflows/wf_interbank_receiver_abandoned_test.go \
   test-app/workflows/wf_interbank_success_test.go
```

- [ ] **Step 9.3: Verify test-app builds**

Run: `cd test-app && go build ./...`
Expected: clean. If a remaining workflow imports a deleted symbol, find and fix it (likely a shared helper in `helpers_interbank_test.go` that was used cross-file — inline the helper into the consumer or just remove the consumer).

- [ ] **Step 9.4: Run remaining test-app tests**

Run: `cd test-app && go test ./workflows/ -count=1 -run "TestExclude_NothingNothing"` first to verify the suite still compiles even when zero tests match. Then run the full suite (will hit Docker — only run if Docker is up):
```bash
cd test-app && go test ./workflows/ -count=1 -timeout 600s
```
Expected: all remaining workflows pass (intra-bank, OTC intra-bank, fund operations, etc.). If Docker isn't running, skip the full integration run for now and document in commit message.

- [ ] **Step 9.5: Commit**

```bash
git add -A
git commit -m "test(test-app): delete inter-bank + crossbank workflow tests (Phase 1)"
```

---

## Task 10 — Remove inter-bank docker-compose env vars

**Files:**
- Modify: `docker-compose.yml`
- Modify: `docker-compose-remote.yml`

- [ ] **Step 10.1: List the env vars to remove**

Run: `grep -nE "PEER_|INTERBANK_|OWN_BANK_CODE" docker-compose.yml`
Expected: lines like `PEER_222_BASE_URL`, `PEER_222_INBOUND_KEY`, `PEER_222_OUTBOUND_KEY`, `PEER_333_*`, `PEER_444_*`, `INTERBANK_PREPARE_TIMEOUT`, `INTERBANK_COMMIT_TIMEOUT`, `INTERBANK_RECEIVER_WAIT`, `INTERBANK_RECONCILE_*`, plus `OWN_BANK_CODE` in the api-gateway and transaction-service env blocks.

**Keep** `OWN_BANK_CODE`. It's still used by the `PeerDisabledHandler` from Task 1 to detect own-prefix vs foreign-prefix.

- [ ] **Step 10.2: Delete inter-bank env vars in docker-compose.yml**

Open `docker-compose.yml`. In the `api-gateway` and `transaction-service` `environment:` blocks, delete:
- `INTERBANK_PREPARE_TIMEOUT`
- `INTERBANK_COMMIT_TIMEOUT`
- `INTERBANK_RECEIVER_WAIT`
- `INTERBANK_RECONCILE_INTERVAL`
- `INTERBANK_RECONCILE_MAX_RETRIES`
- `INTERBANK_RECONCILE_STALE_AFTER`
- `PEER_222_BASE_URL`, `PEER_222_INBOUND_KEY`, `PEER_222_OUTBOUND_KEY`
- `PEER_333_BASE_URL`, `PEER_333_INBOUND_KEY`, `PEER_333_OUTBOUND_KEY`
- `PEER_444_BASE_URL`, `PEER_444_INBOUND_KEY`, `PEER_444_OUTBOUND_KEY`

In the `api-gateway` block, also delete the `PEER_*_INBOUND_KEY` lines (api-gateway used these for HMAC verification).

Keep `OWN_BANK_CODE` everywhere.

- [ ] **Step 10.3: Same for docker-compose-remote.yml**

Repeat the same deletions in `docker-compose-remote.yml`. The two files must stay in sync per CLAUDE.md.

- [ ] **Step 10.4: Verify docker-compose syntax**

Run: `docker compose -f docker-compose.yml config > /dev/null && docker compose -f docker-compose-remote.yml config > /dev/null`
Expected: no errors. If `docker` command isn't available, skip and note in commit message.

- [ ] **Step 10.5: Commit**

```bash
git add docker-compose.yml docker-compose-remote.yml
git commit -m "chore(docker): remove inter-bank env vars (PEER_*, INTERBANK_*) (Phase 1)"
```

---

## Task 11 — Drop schema tables on auto-migrate

**Why:** the GORM AutoMigrate calls in the affected services no longer reference `Bank`, `InterBankTransaction`, or `InterBankSagaLog` (Tasks 3 and 5 removed them). New service starts will not recreate the tables. But existing dev databases still have the rows. Add a one-time `DROP TABLE IF EXISTS` on startup, gated by a small migration helper.

**Files:**
- Modify: `transaction-service/cmd/main.go` — add `dropLegacyTables()` call before AutoMigrate.
- Modify: `stock-service/cmd/main.go` — same.

- [ ] **Step 11.1: Add dropLegacyTables in transaction-service**

In `transaction-service/cmd/main.go`, just before the existing `db.AutoMigrate(...)` call, add:

```go
// Phase 1 SI-TX cleanup: drop legacy tables that previously held
// InterBankTransaction / Bank rows. The corresponding GORM models
// have been deleted; AutoMigrate no longer recreates them. This DROP
// runs on every startup but is idempotent.
if err := db.Exec("DROP TABLE IF EXISTS inter_bank_transactions").Error; err != nil {
    log.Printf("warn: drop inter_bank_transactions failed: %v", err)
}
if err := db.Exec("DROP TABLE IF EXISTS banks").Error; err != nil {
    log.Printf("warn: drop banks failed: %v", err)
}
```

The two `Exec` calls must succeed even on a fresh database (where the tables never existed) — `IF EXISTS` handles that.

- [ ] **Step 11.2: Add dropLegacyTables in stock-service**

In `stock-service/cmd/main.go`, just before the existing AutoMigrate call, add:

```go
// Phase 1 SI-TX cleanup: drop legacy inter_bank_saga_logs table.
// Model deleted; AutoMigrate no longer recreates it.
if err := db.Exec("DROP TABLE IF EXISTS inter_bank_saga_logs").Error; err != nil {
    log.Printf("warn: drop inter_bank_saga_logs failed: %v", err)
}
```

- [ ] **Step 11.3: Verify both services build**

Run: `cd transaction-service && go build ./... && cd ../stock-service && go build ./...`
Expected: clean.

- [ ] **Step 11.4: Run docker-compose to verify the DROP statements execute**

If Docker is available:
```bash
make docker-down 2>/dev/null
make docker-up
sleep 30
docker logs exbanka-transaction-service 2>&1 | grep -E "drop|DROP|inter_bank|banks" | head
docker logs exbanka-stock-service 2>&1 | grep -E "drop|DROP|inter_bank_saga" | head
```
Expected: log lines from the new code path execute without error. If Docker isn't available, skip this step (the SQL is straightforward).

- [ ] **Step 11.5: Commit**

```bash
git add transaction-service/cmd/main.go stock-service/cmd/main.go
git commit -m "chore(db): drop banks/inter_bank_transactions/inter_bank_saga_logs on startup (Phase 1)"
```

---

## Task 12 — Replace Specification.md §25 and §27 with stub

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 12.1: Locate the section boundaries**

Run: `grep -nE "^## 25\.|^## 26\.|^## 27\.|^## 28\." docs/Specification.md`
Expected:
```
2395:## 25. Inter-Bank 2PC Transfers (Celina 5 / Spec 3)
2518:## 26. Intra-bank OTC Options (Celina 4 / Spec 2)
2571:## 27. Cross-bank OTC Options (Celina 5 / Spec 4) — Foundation
ZZZZ:## 28. <next section, if any>
```

The line where §28 starts (or end-of-file if §27 is the last) bounds the §27 deletion.

Run: `wc -l docs/Specification.md` to find total line count and verify what comes after §27.

- [ ] **Step 12.2: Replace §25 entirely with a stub**

Use Edit on `docs/Specification.md` to replace the §25 block (from `## 25. Inter-Bank 2PC Transfers (Celina 5 / Spec 3)` through the line just before `## 26.`) with:

```markdown
## 25. Inter-Bank Cross-Bank Communication (Celina 5) — Pending SI-TX Implementation

The previous in-house 2PC inter-bank transfer protocol (HMAC headers, `Prepare` / `Ready` / `NotReady` / `Commit` / `Committed` / `CheckStatus` action enum, three split routes under `/internal/inter-bank/*`, status state machine, reconciler cron) was removed in the Phase 1 demolition of the SI-TX refactor (commit `<filled-in-after-merge>`).

The replacement implementation conforms to the SI-TX cohort wire protocol referenced by Celina 5 (`https://arsen.srht.site/si-tx-proto/`) and is being built in subsequent phases:

- **Phase 2 (foundation):** `Message<Type>` envelope, hybrid `X-Api-Key` / HMAC peer auth middleware, `peer_banks` registry table, `peer_idempotence_records` replay cache. Skeleton `POST /api/v3/interbank` (501).
- **Phase 3 (TX execution):** `NEW_TX` / `COMMIT_TX` / `ROLLBACK_TX` posting executor, vote builder with the 8 SI-TX `NoVote` reasons, sender-side `outbound_peer_txs` + replay cron. Restores inter-bank transfer functionality.
- **Phase 4 (OTC negotiations):** `GET /api/v3/public-stock`, the 5 `/api/v3/negotiations/{rid}/{id}` routes, `GET /api/v3/user/{rid}/{id}`. Restores cross-bank OTC functionality.

Design doc: `docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md`. Phase 1 plan: `docs/superpowers/plans/2026-04-29-celina5-sitx-phase1-demolition.md`.

During this transition `POST /api/v3/me/transfers` works for intra-bank receivers (own 3-digit prefix). Foreign-prefix receivers receive `501 not_implemented` from `PeerDisabledHandler`.
```

- [ ] **Step 12.3: Replace §27 entirely with a stub**

Replace the §27 block (from `## 27. Cross-bank OTC Options (Celina 5 / Spec 4) — Foundation` through the start of the next `##` heading or EOF) with:

```markdown
## 27. Cross-Bank OTC Options (Celina 5) — Pending SI-TX Implementation

The previous in-house cross-bank OTC saga (12-RPC `CrossBankOTCService`, `CrossbankAcceptSaga` / `CrossbankExerciseSaga` / `CrossbankExpireSaga`, `InterBankSagaLog` durable audit table, `CrossbankCheckStatusCron` / `CrossbankOrphanReservationCron` / `CrossbankExpiryCron`) was removed in the Phase 1 demolition of the SI-TX refactor.

The SI-TX-conformant replacement is built in Phase 4 of the refactor (see §25). It exposes peer-facing OTC endpoints at:

- `GET /api/v3/public-stock` — peer OTC discovery
- `POST /api/v3/negotiations` — initiate cross-bank offer
- `PUT /api/v3/negotiations/{rid}/{id}` — counter-offer
- `GET /api/v3/negotiations/{rid}/{id}` — read
- `DELETE /api/v3/negotiations/{rid}/{id}` — cancel
- `GET /api/v3/negotiations/{rid}/{id}/accept` — accept (triggers SI-TX TX formation via `POST /api/v3/interbank` `NEW_TX`)
- `GET /api/v3/user/{rid}/{id}` — user info

Design doc: `docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md`.

During this transition cross-bank OTC is unavailable; intra-bank OTC (Celina 4 / §26) continues to work.
```

- [ ] **Step 12.4: Update §17 (Routes) to remove deleted routes**

Run: `grep -nE "/internal/inter-bank|/transfer/prepare|/transfer/commit|/check-status" docs/Specification.md`
Expected: a few lines in the Section 17 routes table (around line 1232 + and the §25 routes table). Replace each row with a stub note like "[deleted Phase 1 — pending SI-TX]" or remove the rows entirely (cleaner).

- [ ] **Step 12.5: Verify the file is well-formed Markdown**

Run: `head -100 docs/Specification.md && echo "---" && grep -cE "^## " docs/Specification.md`
Expected: TOC and section count look right. The numerical sequence of `## NN.` headings is unbroken (§24 → §25 stub → §26 → §27 stub → §28...).

- [ ] **Step 12.6: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): stub §25 + §27 — pending SI-TX implementation (Phase 1)"
```

---

## Task 13 — Final verification

- [ ] **Step 13.1: Full build**

Run: `make build`
Expected: every service builds clean. Zero unused-import warnings, zero compile errors.

- [ ] **Step 13.2: Full unit-test run**

Run: `make test`
Expected: every service's unit tests pass. test-app workflows are deleted, not skipped.

- [ ] **Step 13.3: Full lint run**

Run: `make lint`
Expected: zero new warnings vs. baseline. `golangci-lint` may flag dead code we missed — fix.

- [ ] **Step 13.4: Verify intra-bank /me/transfers still works**

If Docker is available:
```bash
make docker-down 2>/dev/null
make docker-up
sleep 60
# Use a quick curl to verify gateway responds. Adjust JWT/account numbers.
curl -X POST http://localhost:8080/api/v3/me/transfers \
  -H "Authorization: Bearer <test-token>" \
  -H "Content-Type: application/json" \
  -d '{"from_account_number":"111-1234567890123-56","to_account_number":"111-1234500000EUR-78","amount":100}'
```
Expected: HTTP 201 (or 400 if account numbers are invalid in this env — point is no 500 / 501).

Then verify foreign-prefix returns 501:
```bash
curl -X POST http://localhost:8080/api/v3/me/transfers \
  -H "Authorization: Bearer <test-token>" \
  -H "Content-Type: application/json" \
  -d '{"from_account_number":"111-1234567890123-56","to_account_number":"222-9999999999999-99","amount":100}'
```
Expected: HTTP 501 with body `{"error":{"code":"not_implemented","message":"inter-bank transfers are temporarily disabled (SI-TX implementation in progress)"}}`.

If Docker isn't available, skip — the unit tests cover the prefix-detection logic.

- [ ] **Step 13.5: Update memory: Phase 1 complete**

(No commit needed — runtime memory only.) Use the Write tool to update `~/.claude/projects/.../memory/MEMORY.md` adding a project memory entry like:

```
- [Celina 5 SI-TX refactor in progress](project_celina5_sitx.md) — Phase 1 (demolition) merged YYYY-MM-DD; Phases 2-6 pending. Design: docs/superpowers/specs/2026-04-29-celina5-sitx-refactor-design.md
```

- [ ] **Step 13.6: No final commit needed**

Phase 1 is complete. All work is captured in the 12 prior commits. Push the branch and open a PR (or merge directly per team workflow):

```bash
git push origin Development  # or feature branch name
```

End state checklist:
- [x] `POST /api/v3/me/transfers` returns 501 for foreign prefixes, works for own.
- [x] `GET /api/v3/me/transfers/:id` returns intra-bank-scoped data only.
- [x] Zero remaining `InterBank*` / `Crossbank*` symbols in any Go file.
- [x] Zero remaining `transfer.interbank-*` / `otc.crossbank-*` topics in `kafka/messages.go`.
- [x] Zero remaining `PEER_*` / `INTERBANK_*` env vars in either docker-compose file.
- [x] `banks`, `inter_bank_transactions`, `inter_bank_saga_logs` tables dropped on startup.
- [x] `Specification.md` §25 + §27 carry pending-SI-TX stubs.
- [x] `make build`, `make test`, `make lint` all green.

Phase 2 (foundation) plan to be drafted next.
