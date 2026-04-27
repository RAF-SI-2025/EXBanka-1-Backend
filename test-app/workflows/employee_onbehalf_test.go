//go:build integration

package workflows

import (
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestEmployeeOnBehalf_CreateOrder asserts that an admin can place a stock
// buy order on behalf of a client, and that acting_employee_id is recorded.
func TestEmployeeOnBehalf_CreateOrder(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	clientID, _, clientC, _ := setupActivatedClient(t, adminC)

	// Look up the client's account ID.
	acctsResp, err := adminC.GET("/api/v3/accounts?client_id=" + strconv.Itoa(clientID))
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	helpers.RequireStatus(t, acctsResp, 200)
	accounts, ok := acctsResp.Body["accounts"].([]interface{})
	if !ok || len(accounts) == 0 {
		t.Skipf("no accounts for client %d — skipping", clientID)
	}
	first, _ := accounts[0].(map[string]interface{})
	accountID := int(first["id"].(float64))

	// Pick a listing to trade — any stock listing will do.
	listResp, err := adminC.GET("/api/v3/securities/stocks?page=1&page_size=1")
	if err != nil {
		t.Fatalf("list listings: %v", err)
	}
	if listResp.StatusCode != 200 {
		t.Skipf("stocks endpoint returned %d — skipping on-behalf order test", listResp.StatusCode)
	}
	stocks, _ := listResp.Body["stocks"].([]interface{})
	if len(stocks) == 0 {
		t.Skipf("no stock listings seeded — skipping")
	}
	firstStock, _ := stocks[0].(map[string]interface{})
	listing, _ := firstStock["listing"].(map[string]interface{})
	if listing == nil {
		t.Skipf("first stock has no listing yet — skipping")
	}
	listingID := int(listing["id"].(float64))

	// Place the on-behalf order via the employee route.
	resp, err := adminC.POST("/api/v3/orders", map[string]interface{}{
		"client_id":  clientID,
		"account_id": accountID,
		"listing_id": listingID,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("place on-behalf: %v", err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d body=%v", resp.StatusCode, resp.Body)
	}
	// acting_employee_id must be non-zero.
	orderID := int(helpers.GetNumberField(t, resp, "id"))
	actingEmpID := int(helpers.GetNumberField(t, resp, "acting_employee_id"))
	if actingEmpID == 0 {
		t.Error("acting_employee_id is zero — on-behalf audit info not recorded")
	}

	// Wait for the order to fill. Poll under the client's session because
	// the order's user_id is the client (admin only acted on behalf), so
	// the order is invisible on the admin's /me/orders route.
	tryWaitForOrderFill(t, clientC, orderID, 60*time.Second)

	// Confirm the holding shows up in the CLIENT's portfolio (not the
	// employee's). is_done flips on the order row in the same instant the
	// saga's update_holding step commits, so a one-shot read can race the
	// fill commit; poll for up to 5s.
	var holdings []interface{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		portResp, err := clientC.GET("/api/v3/me/portfolio")
		if err != nil {
			t.Fatalf("client portfolio: %v", err)
		}
		helpers.RequireStatus(t, portResp, 200)
		holdings, _ = portResp.Body["holdings"].([]interface{})
		if len(holdings) > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(holdings) == 0 {
		t.Errorf("client %d portfolio is empty after on-behalf buy", clientID)
	}
}

// TestEmployeeOnBehalf_AccountNotOwnedByClient_Returns403 asserts that placing
// an order for client Alice using Bob's account is rejected with 403.
func TestEmployeeOnBehalf_AccountNotOwnedByClient_Returns403(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	aliceID, _, _, _ := setupActivatedClient(t, adminC)
	bobID, _, _, _ := setupActivatedClient(t, adminC)

	// Look up Bob's account ID.
	acctsResp, err := adminC.GET("/api/v3/accounts?client_id=" + strconv.Itoa(bobID))
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if acctsResp.StatusCode != 200 {
		t.Skipf("list accounts returned %d", acctsResp.StatusCode)
	}
	bobAccts, _ := acctsResp.Body["accounts"].([]interface{})
	if len(bobAccts) == 0 {
		t.Skip("bob has no accounts")
	}
	bobFirst, _ := bobAccts[0].(map[string]interface{})
	bobAccountID := int(bobFirst["id"].(float64))

	// Get any listing.
	listResp, err := adminC.GET("/api/v3/securities/stocks?page=1&page_size=1")
	if err != nil {
		t.Fatalf("list listings: %v", err)
	}
	if listResp.StatusCode != 200 {
		t.Skip("no stocks endpoint")
	}
	stocks, _ := listResp.Body["stocks"].([]interface{})
	if len(stocks) == 0 {
		t.Skip("no listings")
	}
	firstStock, _ := stocks[0].(map[string]interface{})
	listing, _ := firstStock["listing"].(map[string]interface{})
	if listing == nil {
		t.Skip("first stock has no listing yet")
	}
	listingID := int(listing["id"].(float64))

	// Admin tries to place an order for Alice using Bob's account → 403.
	resp, err := adminC.POST("/api/v3/orders", map[string]interface{}{
		"client_id":  aliceID,
		"account_id": bobAccountID,
		"listing_id": listingID,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 account-does-not-belong-to-client, got %d body=%v", resp.StatusCode, resp.Body)
	}
}

// TestEmployeeOnBehalf_AsBasic_Forbidden asserts that an EmployeeBasic role
// (no orders.place-on-behalf permission) cannot place an on-behalf order.
func TestEmployeeOnBehalf_AsBasic_Forbidden(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, basicC, _ := setupBasicEmployee(t, adminC)
	clientID, _, _, _ := setupActivatedClient(t, adminC)

	acctsResp, err := adminC.GET("/api/v3/accounts?client_id=" + strconv.Itoa(clientID))
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if acctsResp.StatusCode != 200 {
		t.Skipf("list accounts returned %d", acctsResp.StatusCode)
	}
	accts, _ := acctsResp.Body["accounts"].([]interface{})
	if len(accts) == 0 {
		t.Skip("no accounts for client")
	}
	first, _ := accts[0].(map[string]interface{})
	accountID := int(first["id"].(float64))

	listResp, err := adminC.GET("/api/v3/securities/stocks?page=1&page_size=1")
	if err != nil {
		t.Fatalf("list listings: %v", err)
	}
	if listResp.StatusCode != 200 {
		t.Skip("no listings endpoint")
	}
	stocks, _ := listResp.Body["stocks"].([]interface{})
	if len(stocks) == 0 {
		t.Skip("no listings seeded")
	}
	listing, _ := stocks[0].(map[string]interface{})["listing"].(map[string]interface{})
	if listing == nil {
		t.Skip("first stock has no listing yet")
	}
	listingID := int(listing["id"].(float64))

	resp, err := basicC.POST("/api/v3/orders", map[string]interface{}{
		"client_id":  clientID,
		"account_id": accountID,
		"listing_id": listingID,
		"direction":  "buy",
		"order_type": "market",
		"quantity":   1,
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 (basic lacks orders.place-on-behalf), got %d body=%v", resp.StatusCode, resp.Body)
	}
}
