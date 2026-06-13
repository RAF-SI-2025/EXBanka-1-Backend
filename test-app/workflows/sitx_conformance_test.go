//go:build integration

package workflows

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturedMsg is a single raw SI-TX envelope the gateway POSTed to the mock
// peer's /interbank endpoint, paired with its decoded messageType for routing.
type capturedMsg struct {
	messageType string
	raw         json.RawMessage
}

// TestSITXConformance_OutboundNewTxIsSpecShaped is the SI-TX wire-conformance
// integration test (Task 18). It reuses the cohort-dry-run harness shape:
// register a mock peer bank, stand up an httptest server acting as the foreign
// bank's /interbank endpoint to CAPTURE the gateway's outbound request, fund an
// activated client, and post a foreign-prefix payment so the gateway dispatches
// a cross-bank NEW_TX (and, after a YES vote, a COMMIT_TX) to the mock peer.
//
// Unlike cohort_dry_run (which only smoke-tests that *a* request arrives), this
// test asserts the captured JSON is SPEC-SHAPED per the SI-TX-Proto spec:
//   - envelope: idempotenceKey{routingNumber, locallyGeneratedKey}, messageType,
//     message object;
//   - NEW_TX message: transactionId{routingNumber, id}, postings[] where each
//     posting has a tagged-union account, a JSON-NUMBER amount (one negative, one
//     positive leg), and a tagged-union asset; plus paymentCode/paymentPurpose
//     keys;
//   - COMMIT_TX correlation: the mock replies 200 {"vote":"YES"} to NEW_TX, the
//     gateway then POSTs a COMMIT_TX whose message.transactionId.id equals the
//     NEW_TX transactionId.id, and whose envelope idempotenceKey.locallyGeneratedKey
//     DIFFERS from the NEW_TX one (per-message unique idempotence key).
//
// The gateway dispatches NEW_TX→COMMIT_TX synchronously inside InitiateOutboundTx
// (transaction-service/internal/handler/peer_tx_grpc_handler.go) when the peer
// votes YES, so both messages land on the mock within the same request flow —
// no replay-cron wait is needed.
//
// REQUIRES A LIVE STACK. This test is gated behind the `integration` build tag
// and skips unless the gateway is reachable on localhost:8080 (i.e. run
// `make docker-up` first). It is executed by cohort CI, not by `go test` here.
func TestSITXConformance_OutboundNewTxIsSpecShaped(t *testing.T) {
	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not running on localhost:8080 (run `make docker-up` first): %v", err)
	}

	// 1. Boot a mock peer bank whose /interbank endpoint CAPTURES every envelope
	//    the gateway POSTs, then replies per SI-TX:
	//      NEW_TX    → 200 {"vote":"YES"}  (so the gateway proceeds to COMMIT_TX)
	//      COMMIT_TX → 204 No Content
	//      ROLLBACK_TX → 204 No Content
	var (
		mu       sync.Mutex
		captured []capturedMsg
	)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/interbank" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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
		mu.Lock()
		captured = append(captured, capturedMsg{messageType: head.MessageType, raw: append(json.RawMessage(nil), body...)})
		mu.Unlock()
		t.Logf("mock peer received %s", head.MessageType)
		switch head.MessageType {
		case "NEW_TX":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vote":"YES"}`))
		case "COMMIT_TX", "ROLLBACK_TX":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer mock.Close()
	t.Logf("mock peer URL: %s", mock.URL)

	// 2. Login as admin and register the mock as peer bank 222.
	adminC := loginAsAdmin(t)

	createResp, err := adminC.POST("/api/v3/peer-banks", map[string]interface{}{
		"bank_code":      "222",
		"routing_number": 222,
		"base_url":       mock.URL, // mock listens on /interbank
		"api_token":      "sitx-conformance-token",
		"active":         true,
	})
	if err != nil {
		t.Fatalf("create peer bank: %v", err)
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created from POST /api/v3/peer-banks, got %d body=%s", createResp.StatusCode, string(createResp.RawBody))
	}
	peerBankIDFloat, ok := createResp.Body["id"].(float64)
	if !ok {
		t.Fatalf("missing/non-numeric id in peer-bank create response: %+v", createResp.Body)
	}
	peerBankID := strconv.FormatInt(int64(peerBankIDFloat), 10)
	t.Logf("registered peer bank id=%s", peerBankID)

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

	// 3. Set up an activated client with a funded RSD account.
	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)
	t.Logf("activated client account: %s", accountNumber)

	// 4. Initiate a foreign-prefix PAYMENT (cross-bank money sends are payments).
	//    The receiver account's 3-digit prefix "222" matches the mock peer's
	//    bank_code, so the gateway dispatches inter-bank via SI-TX.
	foreignReceiver := "222999999999999999"
	transferResp, err := clientC.POST("/api/v3/me/payments", map[string]interface{}{
		"from_account_number": accountNumber,
		"to_account_number":   foreignReceiver,
		"amount":              "10.00",
		"currency":            "RSD",
	})
	if err != nil {
		t.Fatalf("create cross-bank payment: %v", err)
	}
	if transferResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for foreign-prefix payment, got %d body=%s", transferResp.StatusCode, string(transferResp.RawBody))
	}
	txID, _ := transferResp.Body["transaction_id"].(string)
	if txID == "" {
		t.Fatalf("missing transaction_id in response: %+v", transferResp.Body)
	}
	t.Logf("dispatched tx %s", txID)

	// 5. Wait for the mock peer to have received both NEW_TX and COMMIT_TX. The
	//    synchronous dispatch fires both within the InitiateOutboundTx call, but
	//    the gateway returns 202 before/around that, so poll briefly.
	newTxRaw := waitForCaptured(t, &mu, &captured, "NEW_TX", 10*time.Second)
	commitRaw := waitForCaptured(t, &mu, &captured, "COMMIT_TX", 10*time.Second)

	// 6. Assert the NEW_TX envelope + message are spec-shaped.
	newTxID := assertNewTxSpecShaped(t, newTxRaw)

	// 7. Assert COMMIT_TX correlation: same transactionId.id, different envelope
	//    idempotence key (per-message unique idem).
	assertCommitCorrelatesToNewTx(t, commitRaw, newTxID, idemKey(t, newTxRaw))

	// NOTE: GET /public-stock and /user are endpoints OUR gateway EXPOSES to
	// peers; the outbound payment flow never makes the gateway CALL the peer's
	// /public-stock or /user, so this capture harness can't observe them. Asserting
	// their bare-array / display-name shapes would need a separate inbound or
	// peer-cache test, which is out of scope here.
	t.Log("skipped /public-stock and /user assertions: not reachable via the outbound-payment capture harness")
}

// waitForCaptured polls the captured-message slice (under mu) until an envelope
// with the given messageType appears, returning its raw JSON, or fails after
// timeout.
func waitForCaptured(t *testing.T, mu *sync.Mutex, captured *[]capturedMsg, messageType string, timeout time.Duration) json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, m := range *captured {
			if m.messageType == messageType {
				raw := m.raw
				mu.Unlock()
				return raw
			}
		}
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mock peer never received a %s within %s", messageType, timeout)
	return nil
}

// idemKey extracts envelope.idempotenceKey.locallyGeneratedKey from a raw SI-TX
// envelope.
func idemKey(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var env map[string]interface{}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("idemKey: envelope is not valid JSON: %v (raw=%s)", err, string(raw))
	}
	ik, ok := env["idempotenceKey"].(map[string]interface{})
	if !ok {
		t.Fatalf("idemKey: envelope missing object idempotenceKey: %s", string(raw))
	}
	k, _ := ik["locallyGeneratedKey"].(string)
	if k == "" {
		t.Fatalf("idemKey: idempotenceKey.locallyGeneratedKey missing/empty: %s", string(raw))
	}
	return k
}

// assertNewTxSpecShaped validates a captured NEW_TX envelope against the SI-TX
// wire spec and returns the message.transactionId.id for correlation.
func assertNewTxSpecShaped(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	var env map[string]interface{}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("NEW_TX: envelope is not valid JSON: %v (raw=%s)", err, string(raw))
	}

	// Top-level: idempotenceKey{routingNumber, locallyGeneratedKey}.
	ik, ok := env["idempotenceKey"].(map[string]interface{})
	if !ok {
		t.Fatalf("NEW_TX: top-level idempotenceKey is not an object: %s", string(raw))
	}
	if _, ok := ik["routingNumber"].(float64); !ok {
		t.Errorf("NEW_TX: idempotenceKey.routingNumber is not a JSON number: %v", ik["routingNumber"])
	}
	if lgk, ok := ik["locallyGeneratedKey"].(string); !ok || lgk == "" {
		t.Errorf("NEW_TX: idempotenceKey.locallyGeneratedKey missing/empty: %v", ik["locallyGeneratedKey"])
	}

	// Top-level: messageType == "NEW_TX".
	if mt, _ := env["messageType"].(string); mt != "NEW_TX" {
		t.Errorf("NEW_TX: messageType = %q, want %q", env["messageType"], "NEW_TX")
	}

	// Top-level: message object.
	msg, ok := env["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("NEW_TX: message is not an object: %s", string(raw))
	}

	// message.transactionId{routingNumber, id} present and non-empty.
	txID, ok := msg["transactionId"].(map[string]interface{})
	if !ok {
		t.Fatalf("NEW_TX: message.transactionId is not an object: %v", msg["transactionId"])
	}
	if _, ok := txID["routingNumber"].(float64); !ok {
		t.Errorf("NEW_TX: message.transactionId.routingNumber is not a JSON number: %v", txID["routingNumber"])
	}
	txIDStr, ok := txID["id"].(string)
	if !ok || txIDStr == "" {
		t.Errorf("NEW_TX: message.transactionId.id missing/empty: %v", txID["id"])
	}

	// message.paymentCode / message.paymentPurpose keys present.
	if _, ok := msg["paymentCode"]; !ok {
		t.Errorf("NEW_TX: message.paymentCode key absent")
	}
	if _, ok := msg["paymentPurpose"]; !ok {
		t.Errorf("NEW_TX: message.paymentPurpose key absent")
	}

	// message.postings is an array.
	postings, ok := msg["postings"].([]interface{})
	if !ok {
		t.Fatalf("NEW_TX: message.postings is not an array: %v", msg["postings"])
	}
	if len(postings) < 2 {
		t.Fatalf("NEW_TX: expected at least 2 postings (a double-entry pair), got %d", len(postings))
	}

	var sawNegative, sawPositive bool
	validAccountTypes := map[string]bool{"PERSON": true, "ACCOUNT": true, "OPTION": true}
	validAssetTypes := map[string]bool{"MONAS": true, "STOCK": true, "OPTION": true}

	for i, p := range postings {
		posting, ok := p.(map[string]interface{})
		if !ok {
			t.Errorf("NEW_TX: posting[%d] is not an object: %v", i, p)
			continue
		}

		// account is a tagged union {type, num|id}.
		account, ok := posting["account"].(map[string]interface{})
		if !ok {
			t.Errorf("NEW_TX: posting[%d].account is not an object: %v", i, posting["account"])
			continue
		}
		acctType, _ := account["type"].(string)
		if !validAccountTypes[acctType] {
			t.Errorf("NEW_TX: posting[%d].account.type = %q, want one of PERSON|ACCOUNT|OPTION", i, acctType)
		}
		switch acctType {
		case "ACCOUNT":
			if num, ok := account["num"].(string); !ok || num == "" {
				t.Errorf("NEW_TX: posting[%d].account.type=ACCOUNT but num missing/empty: %v", i, account["num"])
			}
		case "PERSON", "OPTION":
			id, ok := account["id"].(map[string]interface{})
			if !ok {
				t.Errorf("NEW_TX: posting[%d].account.type=%s but id is not an object (ForeignBankId): %v", i, acctType, account["id"])
				break
			}
			if _, ok := id["routingNumber"].(float64); !ok {
				t.Errorf("NEW_TX: posting[%d].account.id.routingNumber is not a JSON number: %v", i, id["routingNumber"])
			}
			if idStr, ok := id["id"].(string); !ok || idStr == "" {
				t.Errorf("NEW_TX: posting[%d].account.id.id missing/empty: %v", i, id["id"])
			}
		}

		// amount must decode as a JSON NUMBER (not a string).
		amt, ok := posting["amount"].(float64)
		if !ok {
			t.Errorf("NEW_TX: posting[%d].amount is not a JSON number (got %T %v) — spec requires a number, not a string", i, posting["amount"], posting["amount"])
		} else {
			if amt < 0 {
				sawNegative = true
			}
			if amt > 0 {
				sawPositive = true
			}
		}

		// asset is a tagged union {type, asset:{...}}.
		asset, ok := posting["asset"].(map[string]interface{})
		if !ok {
			t.Errorf("NEW_TX: posting[%d].asset is not an object: %v", i, posting["asset"])
			continue
		}
		assetType, _ := asset["type"].(string)
		if !validAssetTypes[assetType] {
			t.Errorf("NEW_TX: posting[%d].asset.type = %q, want one of MONAS|STOCK|OPTION", i, assetType)
		}
		if _, ok := asset["asset"].(map[string]interface{}); !ok {
			t.Errorf("NEW_TX: posting[%d].asset.asset is not an object: %v", i, asset["asset"])
		}
	}

	if !sawNegative {
		t.Errorf("NEW_TX: expected at least one negative-amount posting leg (asset leaving), saw none")
	}
	if !sawPositive {
		t.Errorf("NEW_TX: expected at least one positive-amount posting leg (asset arriving), saw none")
	}

	return txIDStr
}

// assertCommitCorrelatesToNewTx validates that the captured COMMIT_TX references
// the same transactionId.id as the NEW_TX, and that its envelope idempotence key
// differs from the NEW_TX's (per-message unique idempotence key per spec).
func assertCommitCorrelatesToNewTx(t *testing.T, raw json.RawMessage, newTxID, newTxIdemKey string) {
	t.Helper()

	var env map[string]interface{}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("COMMIT_TX: envelope is not valid JSON: %v (raw=%s)", err, string(raw))
	}

	if mt, _ := env["messageType"].(string); mt != "COMMIT_TX" {
		t.Errorf("COMMIT_TX: messageType = %q, want %q", env["messageType"], "COMMIT_TX")
	}

	ik, ok := env["idempotenceKey"].(map[string]interface{})
	if !ok {
		t.Fatalf("COMMIT_TX: idempotenceKey is not an object: %s", string(raw))
	}
	commitIdem, _ := ik["locallyGeneratedKey"].(string)
	if commitIdem == "" {
		t.Errorf("COMMIT_TX: idempotenceKey.locallyGeneratedKey missing/empty")
	}
	if commitIdem == newTxIdemKey {
		t.Errorf("COMMIT_TX: envelope idempotenceKey.locallyGeneratedKey (%q) equals the NEW_TX one — each message must carry a unique idempotence key", commitIdem)
	}

	msg, ok := env["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("COMMIT_TX: message is not an object: %s", string(raw))
	}
	txID, ok := msg["transactionId"].(map[string]interface{})
	if !ok {
		t.Fatalf("COMMIT_TX: message.transactionId is not an object: %v", msg["transactionId"])
	}
	commitTxID, _ := txID["id"].(string)
	if commitTxID == "" {
		t.Errorf("COMMIT_TX: message.transactionId.id missing/empty")
	}
	if commitTxID != newTxID {
		t.Errorf("COMMIT_TX: message.transactionId.id = %q, want %q (must correlate to the NEW_TX transactionId)", commitTxID, newTxID)
	}

	// Defensive: ensure we didn't accidentally compare a numeric routingNumber that
	// got stringified somewhere unexpected.
	if strings.TrimSpace(commitTxID) != commitTxID {
		t.Errorf("COMMIT_TX: transactionId.id has surrounding whitespace: %q", commitTxID)
	}
}
