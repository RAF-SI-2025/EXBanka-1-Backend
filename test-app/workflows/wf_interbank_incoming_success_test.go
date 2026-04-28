//go:build integration

package workflows

import (
	"encoding/json"
	"net/http"
	"strconv"
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

	// Echo the receiver's final terms back in Commit. The receiver applied an
	// incoming-transfer fee, so finalAmount may be < 500. termsMatch requires
	// these to match what was committed in the Prepare row.
	var prepareEnv struct {
		Body struct {
			FinalAmount   string `json:"finalAmount"`
			FinalCurrency string `json:"finalCurrency"`
			FxRate        string `json:"fxRate"`
			Fees          string `json:"fees"`
		} `json:"body"`
	}
	if err := json.Unmarshal(body, &prepareEnv); err != nil {
		t.Fatalf("Prepare: cannot decode envelope body: %v", err)
	}
	commitBody := map[string]any{
		"finalAmount":   prepareEnv.Body.FinalAmount,
		"finalCurrency": prepareEnv.Body.FinalCurrency,
		"fxRate":        prepareEnv.Body.FxRate,
		"fees":          prepareEnv.Body.Fees,
	}
	resp, body = peerSignedPost(t, gateway, "transfer/commit", txID, "Commit", commitBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Commit: status %d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"action":"Committed"`) {
		t.Fatalf("Commit: expected Committed, got %s", string(body))
	}

	// requireBalanceEquals polls — the saga can take a moment to flush the
	// credit through to the account-service ledger after Commit returns.
	wantBalance := addDecimalStrings("100000", prepareEnv.Body.FinalAmount)
	requireBalanceEquals(t, adminC, accountNumber, wantBalance)
}

// addDecimalStrings returns a + b for shopspring/decimal-style strings.
// We don't import shopspring just for this; the values are simple enough
// that strconv parsing + formatting works.
func addDecimalStrings(a, b string) string {
	af, _ := strconv.ParseFloat(a, 64)
	bf, _ := strconv.ParseFloat(b, 64)
	return strconv.FormatFloat(af+bf, 'f', -1, 64)
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
