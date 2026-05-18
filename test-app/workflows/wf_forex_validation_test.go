//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_ForexValidation_RejectsMalformedRequests is the Phase-2 gateway-level
// forex validation guard (Task 18 defense-in-depth). Three misuses all must
// be 400 errors before the request ever hits stock-service:
//
//   - security_type=forex with no base_account_id → 400
//   - security_type=forex with base_account_id == account_id → 400
//   - security_type=forex with direction=sell → 400
//
// These checks live in api-gateway/internal/handler/stock_order_handler.go
// and are re-enforced in stock-service. The gateway validation keeps bad
// requests from ever reserving funds.
func TestWF_ForexValidation_RejectsMalformedRequests(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)
	accountID := getFirstClientAccountID(t, adminC, clientID)

	// Case 1: missing base_account_id on a forex buy → 400 with
	// "forex orders require base_account_id".
	t.Run("missing_base_account_id", func(t *testing.T) {
		resp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
			"security_type": "forex",
			"listing_id":    1,
			"direction":     "buy",
			"order_type":    "market",
			"quantity":      1,
			"account_id":    accountID,
		})
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		helpers.RequireStatus(t, resp, 400)
		helpers.RequireBodyContains(t, resp, "base_account_id")
	})

	// Case 2: base_account_id == account_id → 400 with
	// "base_account_id must differ from account_id".
	t.Run("base_equals_account", func(t *testing.T) {
		resp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
			"security_type":   "forex",
			"listing_id":      1,
			"direction":       "buy",
			"order_type":      "market",
			"quantity":        1,
			"account_id":      accountID,
			"base_account_id": accountID,
		})
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		helpers.RequireStatus(t, resp, 400)
		helpers.RequireBodyContains(t, resp, "base_account_id")
	})

	// Case 3: direction=sell on a forex order → 400 with
	// "forex orders must be direction=buy".
	//
	// We supply a fake holding_id so the generic "holding_id required for
	// sell" check passes and we land on the forex-specific direction check.
	t.Run("direction_sell", func(t *testing.T) {
		resp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
			"security_type":   "forex",
			"listing_id":      1,
			"direction":       "sell",
			"order_type":      "market",
			"quantity":        1,
			"holding_id":      1,
			"account_id":      accountID,
			"base_account_id": accountID + 1, // any other id — validator only checks != account_id
		})
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		helpers.RequireStatus(t, resp, 400)
		helpers.RequireBodyContains(t, resp, "buy")
	})
}
