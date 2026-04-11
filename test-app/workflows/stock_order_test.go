//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestOrder_CreateMarketBuyOrder(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)
	_, listingID := getFirstStockListingID(t, agentC)

	bankAcctResp, err := adminC.GET("/api/bank-accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, bankAcctResp, 200)
	accts := bankAcctResp.Body["accounts"].([]interface{})
	acctID := uint64(accts[0].(map[string]interface{})["id"].(float64))

	resp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
		"account_id":  acctID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "id")
	helpers.RequireFieldEquals(t, resp, "direction", "buy")
	helpers.RequireFieldEquals(t, resp, "order_type", "market")
}

func TestOrder_CreateOrder_Unauthenticated(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/me/orders", map[string]interface{}{
		"listing_id": 1,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestOrder_CreateOrder_InvalidDirection(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id": 1,
		"direction":  "invalid",
		"order_type": "market",
		"quantity":   1,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOrder_CreateOrder_InvalidOrderType(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id": 1,
		"direction":  "buy",
		"order_type": "invalid",
		"quantity":   1,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOrder_CreateOrder_ZeroQuantity(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id": 1,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   0,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOrder_CreateLimitOrder_RequiresLimitValue(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id": 1,
		"direction":  "buy",
		"order_type": "limit",
		"quantity":   1,
		"account_id": 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOrder_CreateBuyOrder_RequiresAccountID(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id": 1,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestOrder_ListMyOrders(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/me/orders")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "orders")
	helpers.RequireField(t, resp, "total_count")
}

func TestOrder_GetMyOrder(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)
	_, listingID := getFirstStockListingID(t, agentC)

	bankAcctResp, err := adminC.GET("/api/bank-accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, bankAcctResp, 200)
	accts := bankAcctResp.Body["accounts"].([]interface{})
	acctID := uint64(accts[0].(map[string]interface{})["id"].(float64))

	createResp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
		"account_id":  acctID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	orderID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := agentC.GET("/api/me/orders/" + helpers.FormatID(orderID))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "id")
	helpers.RequireField(t, resp, "direction")
	helpers.RequireField(t, resp, "order_type")
}

func TestOrder_GetMyOrder_NotFound(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/me/orders/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 404)
}

func TestOrder_CancelOrder(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)
	_, listingID := getFirstStockListingID(t, agentC)

	bankAcctResp, err := adminC.GET("/api/bank-accounts")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, bankAcctResp, 200)
	accts := bankAcctResp.Body["accounts"].([]interface{})
	acctID := uint64(accts[0].(map[string]interface{})["id"].(float64))

	createResp, err := agentC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "limit",
		"quantity":    1,
		"limit_value": 1.00,
		"all_or_none": false,
		"margin":      false,
		"account_id":  acctID,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	orderID := int(helpers.GetNumberField(t, createResp, "id"))

	resp, err := agentC.POST("/api/me/orders/"+helpers.FormatID(orderID)+"/cancel", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Accept either 200 (cancelled) or 409 (already executed/cancelled)
	if resp.StatusCode != 200 && resp.StatusCode != 409 {
		t.Fatalf("expected 200 or 409, got %d", resp.StatusCode)
	}
}

func TestOrder_CancelOrder_NotFound(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/me/orders/999999/cancel", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 404)
}

func TestOrder_ListOrders_RequiresSupervisor(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/orders")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}

func TestOrder_ListOrders_Supervisor(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.GET("/api/orders")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "orders")
}

func TestOrder_ApproveOrder_RequiresSupervisor(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/orders/1/approve", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}

func TestOrder_DeclineOrder_RequiresSupervisor(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/orders/1/decline", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}

func TestOrder_ClientOrderAutoApproved(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)
	_, listingID := getFirstStockListingID(t, clientC)

	resp, err := clientC.POST("/api/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
		"account_id":  1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		status := fmt.Sprintf("%v", resp.Body["status"])
		if status == "pending" {
			t.Fatal("client order should not be pending — should be auto-approved")
		}
	}
}
