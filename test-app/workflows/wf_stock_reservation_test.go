//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_StockBuy_CancelReleasesReservation is a Phase-2 regression guard for
// bug #4 (ledger divergence on cancel).
//
// Shape: client places a limit buy well below market so it stays pending →
// assert reserved_balance rose, available_balance dropped, balance unchanged →
// cancel the order → assert reserved_balance returned to 0, available_balance
// returned to its placement-time value, balance still unchanged.
//
// The invariant: cancelling a never-filled order must be a pure no-op on the
// ledger. Any residue in reserved_balance or change in balance means the
// placement-saga compensation path did not fully roll back the ReserveFunds
// step.
func TestWF_StockBuy_CancelReleasesReservation(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Fresh client + funded RSD account.
	clientID, acctNum, clientC, _ := setupActivatedClient(t, adminC)
	accountID := getFirstClientAccountID(t, adminC, clientID)

	// Pick a stock listing (whatever the simulator has seeded).
	_, listingID := getFirstStockListingID(t, clientC)

	before := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("before: balance=%.4f available=%.4f reserved=%.4f",
		before.Balance, before.Available, before.Reserved)

	// Limit buy at 0.01 (absurdly low) so it reserves funds but stays pending.
	// We reserve at the limit_value; quantity × 0.01 == very small → well under
	// 100 000 RSD starting balance.
	resp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "limit",
		"quantity":    10,
		"limit_value": 0.01,
		"all_or_none": false,
		"margin":      false,
		"account_id":  accountID,
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	orderID := int(helpers.GetNumberField(t, resp, "id"))

	// Give placement saga a moment to settle the ReserveFunds step.
	time.Sleep(2 * time.Second)

	afterPlace := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("after place: balance=%.4f available=%.4f reserved=%.4f",
		afterPlace.Balance, afterPlace.Available, afterPlace.Reserved)

	// Invariant 1: balance unchanged by placement (money is only moved out of
	// the available pool, not debited from the account).
	if fDiff(afterPlace.Balance, before.Balance) > 0.01 {
		t.Errorf("balance changed on placement: before=%.4f after=%.4f (expected unchanged)",
			before.Balance, afterPlace.Balance)
	}

	// Invariant 2: reserved rose. We don't know the exact reservation amount
	// (depends on service-side rounding), so we only assert a positive delta.
	if afterPlace.Reserved <= before.Reserved {
		t.Errorf("reserved did not rise on placement: before=%.4f after=%.4f",
			before.Reserved, afterPlace.Reserved)
	}

	// Cancel the still-pending order.
	cancelResp, err := clientC.POST(fmt.Sprintf("/api/v3/me/orders/%d/cancel", orderID), nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelResp.StatusCode != 200 {
		t.Fatalf("cancel: expected 200 (order was pending), got %d body=%s",
			cancelResp.StatusCode, string(cancelResp.RawBody))
	}

	// Give release-saga a moment.
	time.Sleep(2 * time.Second)

	afterCancel := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("after cancel: balance=%.4f available=%.4f reserved=%.4f",
		afterCancel.Balance, afterCancel.Available, afterCancel.Reserved)

	// Invariant 3: balance STILL unchanged — cancel must not debit funds.
	if fDiff(afterCancel.Balance, before.Balance) > 0.01 {
		t.Errorf("balance changed after cancel: before=%.4f after=%.4f (expected unchanged — bug #4 regression)",
			before.Balance, afterCancel.Balance)
	}

	// Invariant 4: reserved back to pre-placement value. This is the core
	// Phase-2 reservation-release guarantee.
	if fDiff(afterCancel.Reserved, before.Reserved) > 0.01 {
		t.Errorf("reserved not released on cancel: before=%.4f after=%.4f (bug #4 regression)",
			before.Reserved, afterCancel.Reserved)
	}

	// Invariant 5: available restored so the user can spend those funds again.
	if fDiff(afterCancel.Available, before.Available) > 0.01 {
		t.Errorf("available not restored on cancel: before=%.4f after=%.4f",
			before.Available, afterCancel.Available)
	}
}

// fDiff returns |a - b|. Tiny helper to keep the assertions readable above.
func fDiff(a, b float64) float64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}
