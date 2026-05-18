//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_MultiAssetOrderTypes exercises limit orders and multi-asset order placement:
//
//	supervisor places limit buy with very low price → stays pending → cancels it →
//	if futures exist, places market buy for futures → verifies holding.
func TestWF_MultiAssetOrderTypes(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create supervisor (no approval limits)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	// Step 2: Get a stock listing
	_, listingID := getFirstStockListingID(t, supervisorC)
	bankAcctID := getBankRSDAccountID(t, adminC)
	t.Logf("WF-7: using stock listing_id=%d bank_rsd_account_id=%d", listingID, bankAcctID)

	// Step 3: Place limit buy with absurdly low limit_value so it stays pending
	limitResp, err := supervisorC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "limit",
		"quantity":    1,
		"limit_value": "0.01",
		"all_or_none": false,
		"margin":      false,
		"account_id":  bankAcctID,
	})
	if err != nil {
		t.Fatalf("WF-7: create limit order: %v", err)
	}
	helpers.RequireStatus(t, limitResp, 201)
	limitOrderID := int(helpers.GetNumberField(t, limitResp, "id"))
	t.Logf("WF-7: limit order created id=%d", limitOrderID)

	// Give a moment and verify the order is NOT done. A limit buy at 0.01
	// RSD is far below any real ask, so the matching engine must leave it
	// pending. Pre-fix this was flaky (the engine clamped the fill price
	// to LimitValue and filled regardless) — now it's a hard assertion so
	// any regression reintroduces a noisy failure here.
	time.Sleep(2 * time.Second)
	orderResp, err := supervisorC.GET(fmt.Sprintf("/api/v3/me/orders/%d", limitOrderID))
	if err != nil {
		t.Fatalf("WF-7: get limit order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 200)
	if done, _ := orderResp.Body["is_done"].(bool); done {
		t.Fatalf("WF-7: limit order at 0.01 RSD should stay pending but is_done=true — matching engine ignored LimitValue")
	}
	t.Logf("WF-7: limit order still pending as expected")

	// Step 4: Cancel the limit order
	cancelResp, err := supervisorC.POST(fmt.Sprintf("/api/v3/me/orders/%d/cancel", limitOrderID), nil)
	if err != nil {
		t.Fatalf("WF-7: cancel limit order: %v", err)
	}
	// Accept 200 (cancelled) or 409 (already filled/cancelled)
	if cancelResp.StatusCode != 200 && cancelResp.StatusCode != 409 {
		t.Fatalf("WF-7: expected 200 or 409 on cancel, got %d", cancelResp.StatusCode)
	}
	t.Logf("WF-7: limit order cancel response status=%d", cancelResp.StatusCode)

	// Verify cancelled state if it was still pending
	if cancelResp.StatusCode == 200 {
		checkResp, err := supervisorC.GET(fmt.Sprintf("/api/v3/me/orders/%d", limitOrderID))
		if err != nil {
			t.Fatalf("WF-7: get cancelled order: %v", err)
		}
		helpers.RequireStatus(t, checkResp, 200)
		status := fmt.Sprintf("%v", checkResp.Body["status"])
		t.Logf("WF-7: cancelled order status=%s", status)
	}

	// Step 5: If futures are available, place a market buy
	t.Run("FuturesMarketBuy", func(t *testing.T) {
		futuresID := getFirstFuturesID(t, supervisorC) // calls t.Skip if none
		t.Logf("WF-7: using futures_id=%d", futuresID)

		// Futures use listing ID — fetch the listing for this futures contract
		futuresResp, err := supervisorC.GET(fmt.Sprintf("/api/v3/securities/futures?page=1&page_size=1"))
		if err != nil {
			t.Fatalf("WF-7: get futures: %v", err)
		}
		helpers.RequireStatus(t, futuresResp, 200)

		futuresList := futuresResp.Body["futures"].([]interface{})
		futuresObj := futuresList[0].(map[string]interface{})
		futuresListing := futuresObj["listing"].(map[string]interface{})
		futuresListingID := uint64(futuresListing["id"].(float64))

		buyResp, err := supervisorC.POST("/api/v3/me/orders", map[string]interface{}{
			"listing_id":  futuresListingID,
			"direction":   "buy",
			"order_type":  "market",
			"quantity":    1,
			"all_or_none": false,
			"margin":      false,
			"account_id":  bankAcctID,
		})
		if err != nil {
			t.Fatalf("WF-7: create futures buy order: %v", err)
		}
		helpers.RequireStatus(t, buyResp, 201)
		futuresOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
		t.Logf("WF-7: futures order created id=%d", futuresOrderID)

		waitForOrderFill(t, supervisorC, futuresOrderID, 30*time.Second)

		// Verify holding exists
		portfolioResp, err := supervisorC.GET("/api/v3/me/portfolio")
		if err != nil {
			t.Fatalf("WF-7: list portfolio: %v", err)
		}
		helpers.RequireStatus(t, portfolioResp, 200)
		holdings, ok := portfolioResp.Body["holdings"].([]interface{})
		if !ok || len(holdings) == 0 {
			t.Errorf("WF-7: expected at least one holding after futures buy")
		}
		t.Logf("WF-7: portfolio has %d holding(s) after futures buy", len(holdings))
	})
}
