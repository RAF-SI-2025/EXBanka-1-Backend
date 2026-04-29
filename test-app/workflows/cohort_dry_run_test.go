//go:build integration

package workflows

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCohortDryRun is the SI-TX end-to-end smoke test (Phase 6 of the
// Celina 5 SI-TX refactor). Exercises the full sender-side flow:
// admin registers a mock peer bank → activated client posts a
// foreign-prefix transfer → gateway returns 202 with poll URL →
// outbound dispatch reaches the mock peer.
//
// Skipped by default; runs only when COHORT_DRY_RUN_PEER env var is
// set (any non-empty value). Also skips if the gateway isn't running
// on localhost:8080 (i.e. `make docker-up` hasn't been run).
//
// Real cohort dry-run (against a partner team's bank) is the same test
// but with COHORT_DRY_RUN_PEER pointed at the partner's base URL — the
// httptest.Server below is replaced with the real peer, and the registered
// peer-bank row points at that URL.
func TestCohortDryRun(t *testing.T) {
	if os.Getenv("COHORT_DRY_RUN_PEER") == "" {
		t.Skip("set COHORT_DRY_RUN_PEER=mock to enable cohort dry-run integration test")
	}

	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not running on localhost:8080 (run `make docker-up` first): %v", err)
	}

	// 1. Boot mock peer bank.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/interbank":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var head struct {
				MessageType string `json:"messageType"`
			}
			if err := json.Unmarshal(body, &head); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			t.Logf("mock peer received %s", head.MessageType)
			switch head.MessageType {
			case "NEW_TX":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"type":"YES","transactionId":"mock-tx-1"}`))
			case "COMMIT_TX", "ROLLBACK_TX":
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()
	t.Logf("mock peer URL: %s", mock.URL)

	// 2. Login as admin.
	adminC := loginAsAdmin(t)

	// 3. Register the mock as peer bank 222.
	createResp, err := adminC.POST("/api/v3/peer-banks", map[string]interface{}{
		"bank_code":      "222",
		"routing_number": 222,
		"base_url":       mock.URL, // mock listens on /interbank, etc.
		"api_token":      "cohort-dry-run-token",
		"active":         true,
	})
	if err != nil {
		t.Fatalf("create peer bank: %v", err)
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created from POST /api/v3/peer-banks, got %d body=%s", createResp.StatusCode, string(createResp.RawBody))
	}
	// Peer-bank id is uint64 on the wire → JSON number → float64 after parse.
	peerBankIDFloat, ok := createResp.Body["id"].(float64)
	if !ok {
		t.Fatalf("missing/non-numeric id in peer-bank create response: %+v", createResp.Body)
	}
	peerBankID := strconv.FormatInt(int64(peerBankIDFloat), 10)
	t.Logf("registered peer bank id=%s", peerBankID)

	// Cleanup: DELETE the peer bank registration to leave the DB clean.
	defer func() {
		delResp, err := adminC.DELETE("/api/v3/peer-banks/" + peerBankID)
		if err != nil {
			t.Logf("cleanup: delete peer bank: %v", err)
			return
		}
		if delResp.StatusCode != http.StatusNoContent {
			t.Logf("cleanup: unexpected delete status %d body=%s", delResp.StatusCode, string(delResp.RawBody))
		}
	}()

	// 4. Set up an activated client with a funded RSD account.
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	t.Logf("activated client account: %s", accountNumber)

	// 5. Initiate a foreign-prefix transfer.
	// The receiver account number's 3-digit prefix must be "222" (matching
	// the mock peer's bank_code) so the gateway dispatches inter-bank.
	foreignReceiver := "222999999999999999"
	transferResp, err := clientC.POST("/api/v3/me/transfers", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   foreignReceiver,
		"amount":              "10.00",
		"currency":            "RSD",
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	if transferResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for foreign-prefix transfer, got %d body=%s", transferResp.StatusCode, string(transferResp.RawBody))
	}

	// 6. Verify response contains a transaction_id and poll URL.
	txID, ok := transferResp.Body["transaction_id"].(string)
	if !ok || txID == "" {
		t.Fatalf("missing transaction_id in response: %+v", transferResp.Body)
	}
	pollURL, _ := transferResp.Body["poll_url"].(string)
	if pollURL == "" || !strings.HasPrefix(pollURL, "/api/v3/me/transfers/") {
		t.Errorf("unexpected poll_url: %q", pollURL)
	}
	t.Logf("dispatched tx %s (poll: %s)", txID, pollURL)

	// 7. Allow a brief moment for the gateway's best-effort outbound dispatch
	// to reach the mock peer. The mock logs each /interbank hit via t.Logf.
	// (The OutboundReplayCron's 30s tick is a fallback; the synchronous
	// dispatch path inside InitiateOutboundTx should already have fired.)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	<-ctx.Done()
}
