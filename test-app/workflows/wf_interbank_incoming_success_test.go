//go:build integration

package workflows

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestInterBank_IncomingSuccess — the SUT acts as receiver. We sign a
// Prepare envelope as if from peer 222, post to /internal/inter-bank/
// transfer/prepare, then post a matching Commit. The receiver account
// gets credited with the final amount and a credit ledger entry is
// written.
func TestInterBank_IncomingSuccess(t *testing.T) {
	adminC := loginAsAdmin(t)
	// We don't drive the mock peer in this test — we ARE the peer for
	// inbound traffic — but starting it ensures the docker stack's
	// outbound path is wired (in case any reconciler call fires).
	startMockPeerOnHost(t)

	// Set up the receiver account on this bank.
	_, accountNumber, _, _ := setupActivatedClient(t, adminC)

	txID := "tx-incoming-" + randomNonce(8)
	prepareBody := map[string]any{
		"senderAccount":   "2220000000001234",
		"receiverAccount": accountNumber,
		"amount":          "500.00",
		"currency":        "RSD",
		"memo":            "incoming test",
	}
	gateway := cfg.GatewayURL
	resp, body := peerSignedPost(t, gateway, "transfer/prepare", txID, "Prepare", prepareBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Prepare: status %d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"action":"Ready"`) {
		t.Fatalf("Prepare: expected Ready, got %s", string(body))
	}

	commitBody := map[string]any{
		"finalAmount":   "500.00",
		"finalCurrency": "RSD",
		"fxRate":        "1",
		"fees":          "0",
	}
	resp, body = peerSignedPost(t, gateway, "transfer/commit", txID, "Commit", commitBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Commit: status %d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"action":"Committed"`) {
		t.Fatalf("Commit: expected Committed, got %s", string(body))
	}

	// Allow a brief moment for the kafka publish + ledger flush.
	time.Sleep(500 * time.Millisecond)
	requireBalanceEquals(t, adminC, accountNumber, "100500")
}

// randomNonce returns a hex-encoded random byte string of the given length.
// Local helper to keep this file self-contained.
func randomNonce(byteLen int) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, byteLen*2)
	now := time.Now().UnixNano()
	for i := 0; i < byteLen*2; i++ {
		out[i] = hexChars[(now>>uint(i*2))&0xf]
	}
	return string(out)
}
