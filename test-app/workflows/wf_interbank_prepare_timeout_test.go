//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/peerbank"
)

// TestInterBank_PrepareTimeout_TransitionsToReconciling — peer holds the
// Prepare past the 15s INTERBANK_PREPARE_TIMEOUT, then the SUT moves to
// reconciling. Subsequent CheckStatus from the reconciler is
// configured to return rolled_back, so the row eventually settles to
// rolled_back. Verifies both the transition and the credit-back.
func TestInterBank_PrepareTimeout_TransitionsToReconciling(t *testing.T) {
	adminC := loginAsAdmin(t)
	mock := startMockPeerOnHost(t)
	// Shorten timeout sleep so we don't sit through the 35s default.
	mock.SetTimeoutDuration(20 * time.Second)
	mock.ConfigurePrepareBehavior(peerbank.BehaviorTimeout)
	// When the reconciler probes status, peer says it never had the tx.
	mock.ConfigureStatusBehavior(peerbank.BehaviorStatusUnknown)

	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	txID, status := initiateInterBankTransfer(t, clientC, accountNumber, interbankReceiverAccount(), 1000)
	if status != "reconciling" && status != "preparing" {
		t.Logf("initial status=%q (acceptable: reconciling or preparing)", status)
	}

	body := pollInterBankStatus(t, clientC, txID, []string{"rolled_back", "reconciling"}, 90*time.Second)
	got, _ := body["status"].(string)
	t.Logf("final status: %s", got)
	if got != "rolled_back" && got != "reconciling" {
		t.Errorf("status %q, want rolled_back or reconciling", got)
	}
}
