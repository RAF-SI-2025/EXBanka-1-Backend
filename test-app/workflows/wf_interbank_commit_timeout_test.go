//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/peerbank"
)

// TestInterBank_CommitTimeout_TransitionsToReconciling — peer answers
// Ready then black-holes the Commit. The SUT enters reconciling.
// Configure subsequent CheckStatus probes to return committed so the
// reconciler settles the row to committed.
func TestInterBank_CommitTimeout_TransitionsToReconciling(t *testing.T) {
	adminC := loginAsAdmin(t)
	mock := startMockPeerOnHost(t)
	mock.SetTimeoutDuration(20 * time.Second)
	mock.ConfigureReady(peerReadyTermsRSD("1000.0000"))
	mock.ConfigureCommitBehavior(peerbank.BehaviorTimeout)
	// Reconciler probes — peer reports committed → sender catches up.
	mock.ConfigureStatusKnown(peerbank.StatusTerms{
		Role: "receiver", Status: "committed",
		FinalAmount: "1000.0000", FinalCurrency: "RSD",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})

	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	txID, _ := initiateInterBankTransfer(t, clientC, accountNumber, interbankReceiverAccount(), 1000)

	body := pollInterBankStatus(t, clientC, txID, []string{"committed", "reconciling"}, 90*time.Second)
	got, _ := body["status"].(string)
	t.Logf("final status: %s", got)
}
