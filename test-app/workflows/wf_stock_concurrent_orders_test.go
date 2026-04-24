//go:build integration

package workflows

import (
	"sync"
	"testing"
	"time"
)

// TestWF_StockConcurrentOrders_RespectsAvailableBalance is a Phase-2 regression
// guard for bug #4: many buy orders placed concurrently against the same
// account must never over-commit beyond available_balance.
//
// Shape: 20 goroutines each fire a limit-buy for ~balance/4 worth. If the
// reservation system is atomic, at most ~4 should return 201; the rest must
// be rejected with 400/409. We assert that succeeded_count × per_order_cost
// stays ≤ starting balance.
//
// Per Task-20 spec: "exact count depends on race timing; assert
// succeeded × amount <= balance".
func TestWF_StockConcurrentOrders_RespectsAvailableBalance(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, clientC, _ := setupActivatedClient(t, adminC)
	accountID := getFirstClientAccountID(t, adminC, clientID)

	_, listingID := getFirstStockListingID(t, clientC)

	before := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("starting balance=%.4f available=%.4f", before.Balance, before.Available)

	// Size each order to reserve ~25% of available balance. With 20 goroutines
	// going at it, the total nominal demand is ~500% of the account, so we
	// expect ~4 to win; the rest must be rejected by the FOR UPDATE check in
	// account-service.ReserveFunds.
	const (
		numWorkers = 20
		quantity   = 1
	)
	perOrderCost := before.Available / 4.0
	limitPrice := perOrderCost / float64(quantity)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		rejected  int
	)

	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			// Reuse the shared logged-in APIClient across goroutines — Go's
			// net/http.Client is safe for concurrent use, and the token lives
			// inside clientC so all workers present the same identity (the
			// server's reservation atomicity is what we're validating).
			status, _ := placeOrderRaw(t, clientC, map[string]interface{}{
				"listing_id":  listingID,
				"direction":   "buy",
				"order_type":  "limit",
				"quantity":    quantity,
				"limit_value": limitPrice,
				"all_or_none": false,
				"margin":      false,
				"account_id":  accountID,
			})

			mu.Lock()
			defer mu.Unlock()
			switch status {
			case 201:
				succeeded++
			case 400, 409:
				rejected++
			default:
				t.Logf("worker %d: unexpected status %d", workerID, status)
			}
		}(i)
	}
	wg.Wait()

	// Let placement sagas settle.
	time.Sleep(3 * time.Second)

	t.Logf("concurrent placement: succeeded=%d rejected=%d (of %d)", succeeded, rejected, numWorkers)

	// Invariant: total nominal committed funds must not exceed starting
	// available_balance. This is the Phase-2 atomicity guarantee.
	totalCommitted := float64(succeeded) * perOrderCost
	if totalCommitted > before.Available+0.01 {
		t.Errorf("over-commitment: %d orders × %.4f = %.4f exceeds starting available %.4f (bug #4 regression)",
			succeeded, perOrderCost, totalCommitted, before.Available)
	}

	// Invariant: the account's reserved_balance must also not exceed starting
	// available. Double-check via the authoritative ledger read.
	after := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("after storm: balance=%.4f available=%.4f reserved=%.4f",
		after.Balance, after.Available, after.Reserved)
	if after.Available < -0.01 {
		t.Errorf("available_balance went negative: %.4f (reservation atomicity broken)", after.Available)
	}
	if after.Reserved > before.Available+0.01 {
		t.Errorf("reserved %.4f exceeds starting available %.4f (bug #4 regression)",
			after.Reserved, before.Available)
	}
}
