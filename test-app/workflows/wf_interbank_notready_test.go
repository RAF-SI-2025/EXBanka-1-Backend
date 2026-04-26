//go:build integration

package workflows

import (
	"testing"
	"time"
)

// TestInterBank_NotReady_RollsBackAndCreditsBack — peer answers NotReady;
// the sender flow rolls back, credits the sender, and ends in
// rolled_back. Net balance change: zero (debit then credit-back).
func TestInterBank_NotReady_RollsBackAndCreditsBack(t *testing.T) {
	adminC := loginAsAdmin(t)
	mock := startMockPeerOnHost(t)
	mock.ConfigureNotReady("account_not_found")

	_, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	txID, _ := initiateInterBankTransfer(t, clientC, accountNumber, interbankReceiverAccount(), 1000)

	body := pollInterBankStatus(t, clientC, txID, []string{"rolled_back"}, 15*time.Second)
	if reason, _ := body["errorReason"].(string); reason != "account_not_found" {
		t.Errorf("errorReason=%q, want account_not_found", reason)
	}
	requireBalanceEquals(t, adminC, accountNumber, "100000")
}
