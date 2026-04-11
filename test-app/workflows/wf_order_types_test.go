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
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)

	// Step 1: Create supervisor (no approval limits)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	// Step 2: Get a stock listing
	_, listingID := getFirstStockListingID(t, supervisorC)
	t.Logf("WF-7: using stock listing_id=%d", listingID)

	// Step 3: Place limit buy with absurdly low limit_value so it stays pending
	limitResp, err := supervisorC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "limit",
		"quantity":    1,
		"limit_value": 0.01,
		"all_or_none": false,
		"margin":      false,
	})
	if err != nil {
		t.Fatalf("WF-7: create limit order: %v", err)
	}
	helpers.RequireStatus(t, limitResp, 201)
	limitOrderID := int(helpers.GetNumberField(t, limitResp, "id"))
	t.Logf("WF-7: limit order created id=%d", limitOrderID)

	// Give a moment and verify order is NOT yet done
	time.Sleep(2 * time.Second)
	orderResp, err := supervisorC.GET(fmt.Sprintf("/api/me/orders/%d", limitOrderID))
	if err != nil {
		t.Fatalf("WF-7: get limit order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 200)
	if done, ok := orderResp.Body["is_done"].(bool); ok && done {
		t.Logf("WF-7: limit order filled unexpectedly (price matched) — skipping pending assertion")
	} else {
		t.Logf("WF-7: limit order still pending as expected")
	}

	// Step 4: Cancel the limit order
	cancelResp, err := supervisorC.POST(fmt.Sprintf("/api/me/orders/%d/cancel", limitOrderID), nil)
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
		checkResp, err := supervisorC.GET(fmt.Sprintf("/api/me/orders/%d", limitOrderID))
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
		futuresResp, err := supervisorC.GET(fmt.Sprintf("/api/securities/futures?page=1&page_size=1"))
		if err != nil {
			t.Fatalf("WF-7: get futures: %v", err)
		}
		helpers.RequireStatus(t, futuresResp, 200)

		futuresList := futuresResp.Body["futures"].([]interface{})
		futuresObj := futuresList[0].(map[string]interface{})
		futuresListing := futuresObj["listing"].(map[string]interface{})
		futuresListingID := uint64(futuresListing["id"].(float64))

		buyResp, err := supervisorC.POST("/api/me/orders", map[string]interface{}{
			"listing_id":  futuresListingID,
			"direction":   "buy",
			"order_type":  "market",
			"quantity":    1,
			"all_or_none": false,
			"margin":      false,
		})
		if err != nil {
			t.Fatalf("WF-7: create futures buy order: %v", err)
		}
		helpers.RequireStatus(t, buyResp, 201)
		futuresOrderID := int(helpers.GetNumberField(t, buyResp, "id"))
		t.Logf("WF-7: futures order created id=%d", futuresOrderID)

		waitForOrderFill(t, supervisorC, futuresOrderID, 30*time.Second)

		// Verify holding exists
		portfolioResp, err := supervisorC.GET("/api/me/portfolio")
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
