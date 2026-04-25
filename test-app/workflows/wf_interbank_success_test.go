//go:build integration

package workflows

import (
	"testing"
	"time"
)

// TestInterBank_HappyPath_RSDtoRSD — sender posts /api/v3/me/transfers
// with a 222-prefixed receiver account; mock peer responds Ready then
// Committed; SUT transitions to committed and the sender's account is
// debited by the original amount.
func TestInterBank_HappyPath_RSDtoRSD(t *testing.T) {
	adminC := loginAsAdmin(t)
	mock := startMockPeerOnHost(t)

	mock.ConfigureReady(peerReadyTermsRSD("1000.0000"))
	mock.ConfigureCommitOK(peerCommittedTermsRSD("1000.0000"))

	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	txID, _ := initiateInterBankTransfer(t, clientC, accountNumber, interbankReceiverAccount(), 1000)

	body := pollInterBankStatus(t, clientC, txID, []string{"committed"}, 15*time.Second)
	if got, _ := body["status"].(string); got != "committed" {
		t.Fatalf("final status %q, want committed", got)
	}
	requireBalanceEquals(t, adminC, accountNumber, "99000")

	reqs := mock.Requests()
	if len(reqs) < 2 {
		t.Errorf("expected at least 2 mock peer hits (Prepare + Commit), got %d", len(reqs))
	}
}
