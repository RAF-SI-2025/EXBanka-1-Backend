//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/peerbank"
)

// TestInterBank_CommitMismatch_TransitionsToReconciling — peer answers
// Ready, then 409 commit_mismatch on Commit. SUT moves to reconciling.
// (Whether it eventually rolls back vs. catches up depends on the
// reconciler's CheckStatus probe — we just assert it reaches a non-
// terminal-OK state quickly.)
func TestInterBank_CommitMismatch_TransitionsToReconciling(t *testing.T) {
	adminC := loginAsAdmin(t)
	mock := startMockPeerOnHost(t)
	mock.ConfigureReady(peerReadyTermsRSD("1000.0000"))
	mock.ConfigureCommitMismatch("amount_mismatch")
	// Subsequent reconciler probes: peer claims they rolled back.
	mock.ConfigureStatusKnown(peerbank.StatusTerms{
		Role: "receiver", Status: "rolled_back",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})

	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	txID, _ := initiateInterBankTransfer(t, clientC, accountNumber, interbankReceiverAccount(), 1000)

	body := pollInterBankStatus(t, clientC, txID, []string{"rolled_back", "reconciling"}, 30*time.Second)
	got, _ := body["status"].(string)
	t.Logf("final status: %s", got)
}
