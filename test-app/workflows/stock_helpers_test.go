//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// setupAdminEmployee creates an employee with EmployeeAdmin role, activates them,
// and returns the employee ID and an authenticated client.
func setupAdminEmployee(t *testing.T, adminC *client.APIClient) (empID int, empC *client.APIClient, email string) {
	t.Helper()
	email = helpers.RandomEmail()
	password := helpers.RandomPassword()

	createResp, err := adminC.POST("/api/v1/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Admin"),
		"last_name":     helpers.RandomName("Emp"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email,
		"phone":         helpers.RandomPhone(),
		"address":       "Admin St 1",
		"username":      helpers.RandomName("admin"),
		"position":      "administrator",
		"department":    "Management",
		"role":          "EmployeeAdmin",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("setupAdminEmployee: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID = int(helpers.GetNumberField(t, createResp, "id"))

	token := scanKafkaForActivationToken(t, email)
	activateResp, err := newClient().ActivateAccount(token, password)
	if err != nil {
		t.Fatalf("setupAdminEmployee: activate: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	empC = newClient()
	loginResp, err := empC.Login(email, password)
	if err != nil {
		t.Fatalf("setupAdminEmployee: login: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)
	return empID, empC, email
}

// setupAgentEmployee creates an employee with EmployeeAgent role, activates them,
// and returns the employee ID and an authenticated client.
func setupAgentEmployee(t *testing.T, adminC *client.APIClient) (empID int, agentC *client.APIClient, email string) {
	t.Helper()
	email = helpers.RandomEmail()
	password := helpers.RandomPassword()

	createResp, err := adminC.POST("/api/v1/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Agent"),
		"last_name":     helpers.RandomName("Emp"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email,
		"phone":         helpers.RandomPhone(),
		"address":       "Agent St 1",
		"username":      helpers.RandomName("agent"),
		"position":      "agent",
		"department":    "Trading",
		"role":          "EmployeeAgent",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("setupAgentEmployee: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID = int(helpers.GetNumberField(t, createResp, "id"))

	token := scanKafkaForActivationToken(t, email)
	activateResp, err := newClient().ActivateAccount(token, password)
	if err != nil {
		t.Fatalf("setupAgentEmployee: activate: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	agentC = newClient()
	loginResp, err := agentC.Login(email, password)
	if err != nil {
		t.Fatalf("setupAgentEmployee: login: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)
	return empID, agentC, email
}

// setupSupervisorEmployee creates an employee with EmployeeSupervisor role.
func setupSupervisorEmployee(t *testing.T, adminC *client.APIClient) (empID int, supervisorC *client.APIClient, email string) {
	t.Helper()
	email = helpers.RandomEmail()
	password := helpers.RandomPassword()

	createResp, err := adminC.POST("/api/v1/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Super"),
		"last_name":     helpers.RandomName("Visor"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         email,
		"phone":         helpers.RandomPhone(),
		"address":       "Supervisor Ave 1",
		"username":      helpers.RandomName("super"),
		"position":      "supervisor",
		"department":    "Trading",
		"role":          "EmployeeSupervisor",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("setupSupervisorEmployee: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID = int(helpers.GetNumberField(t, createResp, "id"))

	token := scanKafkaForActivationToken(t, email)
	activateResp, err := newClient().ActivateAccount(token, password)
	if err != nil {
		t.Fatalf("setupSupervisorEmployee: activate: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	supervisorC = newClient()
	loginResp, err := supervisorC.Login(email, password)
	if err != nil {
		t.Fatalf("setupSupervisorEmployee: login: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)
	return empID, supervisorC, email
}

// getFirstStockListingID fetches stocks and returns the first stock's ID and listing_id.
// Assumes stock-service has seeded data.
func getFirstStockListingID(t *testing.T, c *client.APIClient) (stockID uint64, listingID uint64) {
	t.Helper()

	// Retry a few times — the seeder may still be populating listings.
	for attempt := 0; attempt < 10; attempt++ {
		resp, err := c.GET("/api/v1/securities/stocks?page=1&page_size=1")
		if err != nil {
			t.Fatalf("getFirstStockListingID: %v", err)
		}
		helpers.RequireStatus(t, resp, 200)

		stocks, ok := resp.Body["stocks"].([]interface{})
		if !ok || len(stocks) == 0 {
			time.Sleep(3 * time.Second)
			continue
		}
		stock := stocks[0].(map[string]interface{})

		listing, ok := stock["listing"].(map[string]interface{})
		if !ok || listing == nil {
			t.Logf("getFirstStockListingID: stock has no listing yet, retrying (%d/10)...", attempt+1)
			time.Sleep(3 * time.Second)
			continue
		}

		stockID = uint64(stock["id"].(float64))
		listingID = uint64(listing["id"].(float64))
		return
	}
	t.Fatal("getFirstStockListingID: no stock with listing found after 10 attempts")
	return
}

// getFirstFuturesID fetches futures and returns the first futures contract's ID.
func getFirstFuturesID(t *testing.T, c *client.APIClient) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/securities/futures?page=1&page_size=1")
	if err != nil {
		t.Fatalf("getFirstFuturesID: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	futures, ok := resp.Body["futures"].([]interface{})
	if !ok || len(futures) == 0 {
		t.Skip("no futures contracts found — skipping")
	}
	return uint64(futures[0].(map[string]interface{})["id"].(float64))
}

// getFirstForexPairID fetches forex pairs and returns the first pair's ID.
func getFirstForexPairID(t *testing.T, c *client.APIClient) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/securities/forex?page=1&page_size=1")
	if err != nil {
		t.Fatalf("getFirstForexPairID: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	pairs, ok := resp.Body["forex_pairs"].([]interface{})
	if !ok || len(pairs) == 0 {
		t.Skip("no forex pairs found — skipping")
	}
	return uint64(pairs[0].(map[string]interface{})["id"].(float64))
}

// getFirstOptionID fetches options for a stock and returns the first option's ID.
func getFirstOptionID(t *testing.T, c *client.APIClient, stockID uint64) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v1/securities/options?stock_id=" + helpers.FormatID(int(stockID)) + "&page_size=1")
	if err != nil {
		t.Fatalf("getFirstOptionID: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	options, ok := resp.Body["options"].([]interface{})
	if !ok || len(options) == 0 {
		t.Skip("no options found for stock — skipping")
	}
	return uint64(options[0].(map[string]interface{})["id"].(float64))
}
