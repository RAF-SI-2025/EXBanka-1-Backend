//go:build integration

package workflows

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestInterBank_ReceiverAbandoned — peer (us, simulated by signed POST)
// sends Prepare; the SUT replies Ready and reserves the credit. Peer
// never sends Commit. After INTERBANK_RECEIVER_WAIT (10s in compose),
// the receiver-timeout cron releases the reservation and transitions
// the receiver row to abandoned.
//
// Verifies:
//   - balance is unchanged (credit was reserved-only, never committed).
//   - subsequent Commit on the same tx returns 404 (per spec §9.5).
func TestInterBank_ReceiverAbandoned(t *testing.T) {
	adminC := loginAsAdmin(t)
	startMockPeerOnHost(t)

	_, accountNumber, _, _ := setupActivatedClient(t, adminC)
	balanceBefore := "100000"

	txID := "tx-abandon-" + randomHexNonce(t)[:12]
	prepareBody := map[string]any{
		"senderAccount":   "2220000000007777",
		"receiverAccount": accountNumber,
		"amount":          "300.00",
		"currency":        "RSD",
	}
	gateway := cfg.GatewayURL
	resp, body := peerSignedPost(t, gateway, "transfer/prepare", txID, "Prepare", prepareBody)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"action":"Ready"`) {
		t.Fatalf("Prepare: status %d body=%s", resp.StatusCode, body)
	}

	// Wait for receiver-timeout cron to fire (compose default 10s wait + 5s tick).
	time.Sleep(20 * time.Second)

	requireBalanceEquals(t, adminC, accountNumber, balanceBefore)

	// Belated Commit should now 404.
	commitBody := map[string]any{"finalAmount": "300.00", "finalCurrency": "RSD", "fxRate": "1", "fees": "0"}
	resp, body = peerSignedPost(t, gateway, "transfer/commit", txID, "Commit", commitBody)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("late Commit: status %d body=%s, want 404", resp.StatusCode, body)
	}
}
