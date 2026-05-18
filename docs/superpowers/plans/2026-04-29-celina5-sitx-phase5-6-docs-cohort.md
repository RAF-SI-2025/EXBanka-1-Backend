# Celina 5 SI-TX Refactor — Phase 5+6 (Docs + Cohort Dry-Run) Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Phase 1 stubs in `Specification.md` §25 and §27 with full SI-TX implementation descriptions matching what landed in Phases 2-4. Add a cohort dry-run integration test in `test-app/workflows/` that exercises the full SI-TX flow against a local mock peer (skipped by default; gated on `COHORT_DRY_RUN_PEER` env).

**Architecture:** Phase 5 is pure docs. Phase 6 adds one integration test that boots a `httptest.Server` posing as a peer bank (handles `/interbank` and the OTC `/negotiations/...` routes), drives a full transfer + an OTC accept through the api-gateway against this mock, and asserts the final state.

**End state:**
- `Specification.md` §25 + §27 describe the SI-TX implementation in detail (entities, RPCs, routes, auth, retry policy, NoVote reasons).
- `Specification.md` §17 (Routes) lists the 11 new SI-TX peer-facing + admin routes.
- `Specification.md` §18 (Entities) lists the 4 new tables: `peer_banks`, `peer_idempotence_records`, `outbound_peer_txs`, `peer_otc_negotiations`.
- `Specification.md` §6 (Permissions) lists `peer_banks.manage.any`.
- `test-app/workflows/cohort_dry_run_test.go` exists and passes when `COHORT_DRY_RUN_PEER=mock` is set.
- `make build / test / lint` clean.

---

## Phase 5 — Documentation rewrites (3 tasks)

### Task 1 — Rewrite `Specification.md` §25

**File:** `docs/Specification.md`

Replace the Phase 1 stub at §25 with the real SI-TX implementation description. Include:

- Auth scheme (hybrid: X-Api-Key OR HMAC bundle). Cross-reference `peer_banks` table.
- Public peer-facing route: `POST /api/v3/interbank` accepting `Message<Type>` envelope.
- Admin REST routes: `GET/POST/PUT/DELETE /api/v3/peer-banks` (5 routes, gated by `peer_banks.manage.any`).
- Internal gRPC services: `PeerBankAdminService` (5 admin + 2 internal-resolve RPCs), `PeerTxService` (4 RPCs).
- 8 NoVote reason codes with what each means.
- Retry policy: 4 attempts, 60s/120s/240s/480s backoff via `OutboundReplayCron`.
- Sender-debit-immediate semantics preserved.
- Idempotence: `(peer_bank_code, locally_generated_key)` composite-unique on `peer_idempotence_records`.
- Reference Phases 2 & 3 plans + design doc.

Length: 80-120 lines (similar shape to existing intra-bank §26 OTC section).

- [ ] **Step 1.1: Read current §25 + §26 for context**

```bash
grep -n "^## 25\.\|^## 26\." docs/Specification.md
sed -n '<25-line>,<26-line>p' docs/Specification.md
```

- [ ] **Step 1.2: Replace §25 in-place** with the full implementation description (matching the structure: overview → Auth → REST routes → gRPC → DB tables → Retry/Idempotence → Reference).

- [ ] **Step 1.3: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): rewrite §25 for SI-TX implementation (Phase 5 Task 1)"
```

### Task 2 — Rewrite `Specification.md` §27

Same pattern, for the cross-bank OTC section. Cover:
- Cross-bank OTC peer routes: `GET /public-stock`, `POST/PUT/GET/DELETE /api/v3/negotiations/{rid}/{id}`, `GET /negotiations/{rid}/{id}/accept`, `GET /user/{rid}/{id}`.
- `peer_otc_negotiations` table (receiver-side persistence).
- AcceptNegotiation flow: 4-posting Transaction (premium money + OptionDescription both directions) dispatched via `InitiateOutboundTxWithPostings`.
- `PeerOTCService` gRPC service in stock-service.
- Reference Phase 4 plan + design doc.

- [ ] **Step 2.1: Replace §27 in-place**, length 60-100 lines.

- [ ] **Step 2.2: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): rewrite §27 for SI-TX cross-bank OTC implementation (Phase 5 Task 2)"
```

### Task 3 — Update §17 (Routes), §18 (Entities), §6 (Permissions)

- [ ] **Step 3.1: §17** — append rows to the routes table for:
  - `POST /api/v3/interbank` (PeerAuth)
  - `GET /api/v3/public-stock` (PeerAuth)
  - 5 negotiation routes (PeerAuth)
  - `GET /api/v3/user/:rid/:id` (PeerAuth)
  - 5 admin routes `/api/v3/peer-banks` (employee JWT + `peer_banks.manage.any`)

- [ ] **Step 3.2: §18** — append rows to the entities table for `PeerBank`, `PeerIdempotenceRecord`, `OutboundPeerTx`, `PeerOtcNegotiation`. One-line summary each.

- [ ] **Step 3.3: §6** — append `peer_banks.manage.any` to the permissions list with default-roles annotation.

- [ ] **Step 3.4: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): update §17/§18/§6 with SI-TX routes + entities + permission (Phase 5 Task 3)"
```

---

## Phase 6 — Cohort dry-run integration test (1 task)

### Task 4 — `test-app/workflows/cohort_dry_run_test.go`

A single integration test that:
1. Boots a `httptest.Server` posing as peer bank `222`. The mock handles:
   - `POST /interbank` — for NEW_TX returns `{"type":"YES","transactionId":"mock-tx-1"}`; for COMMIT_TX returns 204.
   - `GET /public-stock` — returns 1 stock entry.
   - `POST /negotiations` — returns a negotiation id.
   - `GET /negotiations/:rid/:id/accept` — returns a tx id.
2. Seeds a `peer_banks` row pointing at the mock server URL.
3. Posts a foreign-prefix transfer via `POST /api/v3/me/transfers`. Asserts 202 + `transaction_id` returned.
4. Polls `outbound_peer_txs` until status reaches `committed` (or 10s timeout).
5. Skipped by default; runs when `COHORT_DRY_RUN_PEER=mock` is set.

The test is integration-flavoured but doesn't need Docker — uses the gateway's gRPC clients directly via `bufconn` or via the running stack if Docker is up. Lighter approach: use `bufconn` for transaction-service + the mock httptest server for the peer.

This test is large (~300-400 lines) but self-contained.

- [ ] **Step 4.1: Implement** — see implementation guide below.

- [ ] **Step 4.2: Test runs in skipped mode by default**

```bash
cd test-app && go test ./workflows/ -run "CohortDryRun" -count=1
```
Expected: SKIP message ("set COHORT_DRY_RUN_PEER=mock to enable").

- [ ] **Step 4.3: Test runs end-to-end with env var set**

```bash
COHORT_DRY_RUN_PEER=mock cd test-app && go test ./workflows/ -run "CohortDryRun" -count=1 -v
```
Expected: PASS (assuming docker stack is running; otherwise the test should self-skip on docker-unavailable).

- [ ] **Step 4.4: Commit**

```bash
git add test-app/workflows/cohort_dry_run_test.go
git commit -m "test(test-app): add SI-TX cohort dry-run integration test (Phase 6 Task 4)"
```

### Task 4 implementation guide

Use the existing `test-app/workflows/helpers_*.go` pattern. The test is structurally similar to a heavyweight E2E test: it requires Docker to be up, full stack running, and admin JWT.

If Docker is not running, the test should `t.Skip(...)` with a clear message. Detection: try to dial `localhost:8080` with a 1s timeout — skip on failure.

```go
package workflows_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestCohortDryRun(t *testing.T) {
	if os.Getenv("COHORT_DRY_RUN_PEER") == "" {
		t.Skip("set COHORT_DRY_RUN_PEER=mock to enable cohort dry-run integration test")
	}

	// 1. Verify gateway is reachable.
	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not running on localhost:8080 (need `make docker-up`); skipping: %v", err)
	}

	// 2. Boot mock peer.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/interbank":
			var head struct {
				MessageType string `json:"messageType"`
			}
			body, _ := readAll(r)
			_ = json.Unmarshal(body, &head)
			r.Body = wrap(body) // restore for any downstream
			switch head.MessageType {
			case "NEW_TX":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"type":"YES","transactionId":"mock-tx-1"}`))
			case "COMMIT_TX", "ROLLBACK_TX":
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		case "/public-stock":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stocks":[{"ownerId":{"routingNumber":222,"id":"client-x"},"ticker":"AAPL","amount":50,"pricePerStock":"180.50","currency":"USD"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()

	// 3. Login as admin to get JWT.
	jwt := loginAsAdmin(t)

	// 4. Register the mock as peer bank 222 via /api/v3/peer-banks.
	createPeerBank(t, jwt, mock.URL, "222", 222)

	// 5. Initiate a transfer with foreign-prefix receiver.
	body, _ := json.Marshal(map[string]any{
		"from_account_number": "111000000000000001", // existing seeded account
		"to_account_number":   "222999999999999999",
		"amount":              "100.00",
		"currency":            "RSD",
	})
	resp := postJSON(t, "http://localhost:8080/api/v3/me/transfers", body, jwt)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	var initResp struct {
		TransactionID string `json:"transaction_id"`
		Status        string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&initResp)
	if initResp.TransactionID == "" {
		t.Fatalf("empty transaction_id")
	}

	// 6. Wait until the dispatch happens (best-effort; the gateway's
	// best-effort dispatch should already have hit the mock by now,
	// but cron-driven retries may take up to 30s).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Logf("dispatched tx %s", initResp.TransactionID)
	_ = ctx
}

// helpers — admin login, peer-bank create, request helpers — would
// reuse functions from existing test-app helpers if present, otherwise
// define inline. Keep this test file self-contained.

func readAll(r *http.Request) ([]byte, error)  { /* read r.Body */ return nil, nil }
func wrap(b []byte) any                         { return nil }
func loginAsAdmin(t *testing.T) string          { /* POST /auth/login */ return "" }
func createPeerBank(t *testing.T, jwt, baseURL, code string, rn int64) { /* POST /peer-banks */ }
func postJSON(t *testing.T, url string, body []byte, jwt string) *http.Response {
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}

var _ = fmt.Sprintf // avoid unused-import on minimal stub
```

The above is a skeleton. Flesh out the helpers (admin login via `/api/v3/auth/login` with seed credentials, peer-bank creation via the admin route) using the patterns from existing `test-app/workflows/` files (e.g., `wf_interbank_*` were in this repo before deletion — check git history if needed, or copy from `helpers_test.go` if present).

If Docker isn't easily testable in CI, the test stays skipped and serves as documentation for how to run a real cohort dry-run.

---

## Final task — verification

- [ ] `make build / test / lint` workspace-wide.
- [ ] Update memory: Phase 5+6 complete, project closed.
- [ ] Done.
