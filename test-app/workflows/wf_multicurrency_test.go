//go:build integration

package workflows

import (
	"testing"
)

// TestWF_MultiCurrencyClientLifecycle exercises the cross-currency transfer path:
//
//	client created with RSD (100k) + EUR (10k) accounts →
//	transfer 10000 RSD → EUR → verify: RSD decreased >=10000, EUR increased >0,
//	bank gained commission (>=0).
func TestWF_MultiCurrencyClientLifecycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create client with RSD + EUR accounts
	_, rsdAcct, eurAcct, clientC, _ := setupActivatedClientWithForeignAccount(t, adminC, "EUR")
	t.Logf("WF-2: RSD acct=%s, EUR acct=%s", rsdAcct, eurAcct)

	// Step 2: Record balances before transfer
	rsdBalBefore := getAccountBalance(t, adminC, rsdAcct)
	eurBalBefore := getAccountBalance(t, adminC, eurAcct)
	_, bankBalBefore := getBankRSDAccount(t, adminC)

	// Step 3: Transfer 10000 RSD → EUR
	const transferAmount = 10000.0
	transferID := createAndExecuteTransfer(t, clientC, rsdAcct, eurAcct, transferAmount)
	t.Logf("WF-2: transfer executed id=%d", transferID)

	// Step 4: Assert RSD decreased by >= 10000
	rsdBalAfter := getAccountBalance(t, adminC, rsdAcct)
	rsdDecrease := rsdBalBefore - rsdBalAfter
	if rsdDecrease < transferAmount-0.01 {
		t.Errorf("WF-2: RSD decreased by %.2f, expected >= %.2f", rsdDecrease, transferAmount)
	}

	// Assert EUR increased by > 0
	eurBalAfter := getAccountBalance(t, adminC, eurAcct)
	eurIncrease := eurBalAfter - eurBalBefore
	if eurIncrease <= 0 {
		t.Errorf("WF-2: EUR balance should have increased, got delta=%.2f (before=%.2f, after=%.2f)",
			eurIncrease, eurBalBefore, eurBalAfter)
	}

	// Step 5: Assert bank gained commission (>= 0)
	_, bankBalAfter := getBankRSDAccount(t, adminC)
	bankGain := bankBalAfter - bankBalBefore
	if bankGain < -0.01 {
		t.Errorf("WF-2: bank balance should not have decreased, got delta=%.2f", bankGain)
	}

	t.Logf("WF-2: PASS — RSD %.2f→%.2f (delta=%.2f), EUR %.2f→%.2f (delta=%.2f), bank commission=%.2f",
		rsdBalBefore, rsdBalAfter, rsdDecrease, eurBalBefore, eurBalAfter, eurIncrease, bankGain)
}
