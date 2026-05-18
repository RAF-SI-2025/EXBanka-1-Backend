//go:build integration

package workflows

import (
	"strconv"
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

	createResp, err := adminC.POST("/api/v3/employees", map[string]interface{}{
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

	createResp, err := adminC.POST("/api/v3/employees", map[string]interface{}{
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

	createResp, err := adminC.POST("/api/v3/employees", map[string]interface{}{
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

// setupBasicEmployee creates an employee with EmployeeBasic role, activates them,
// and returns the employee ID and an authenticated client. EmployeeBasic does
// NOT have the orders.place-on-behalf permission and is used in tests that
// verify forbidden responses.
func setupBasicEmployee(t *testing.T, adminC *client.APIClient) (empID int, basicC *client.APIClient, email string) {
	t.Helper()
	email = helpers.RandomEmail()
	password := helpers.RandomPassword()

	createResp, err := adminC.POST("/api/v3/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Basic"),
		"last_name":     helpers.RandomName("Emp"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email,
		"phone":         helpers.RandomPhone(),
		"address":       "Basic St 1",
		"username":      helpers.RandomName("basic"),
		"position":      "teller",
		"department":    "Branch",
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("setupBasicEmployee: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID = int(helpers.GetNumberField(t, createResp, "id"))

	token := scanKafkaForActivationToken(t, email)
	activateResp, err := newClient().ActivateAccount(token, password)
	if err != nil {
		t.Fatalf("setupBasicEmployee: activate: %v", err)
	}
	helpers.RequireStatus(t, activateResp, 200)

	basicC = newClient()
	loginResp, err := basicC.Login(email, password)
	if err != nil {
		t.Fatalf("setupBasicEmployee: login: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)
	return empID, basicC, email
}

// getFirstStockListingID fetches stocks and returns the first stock's ID and listing_id.
// Assumes stock-service has seeded data. Skips stocks that cannot be traded:
//   - no resolvable listing_id (simulator hasn't seeded it),
//   - listing price == 0 (reservation math needs a positive price),
//   - exchange currency not in the supported set (transaction-service rejects
//     unsupported currencies with a 400 on the buy path).
//
// Supported currencies are the bank-account-seedable set:
// RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD.
func getFirstStockListingID(t *testing.T, c *client.APIClient) (stockID uint64, listingID uint64) {
	t.Helper()

	supported := map[string]bool{
		"RSD": true, "EUR": true, "CHF": true, "USD": true,
		"GBP": true, "JPY": true, "CAD": true, "AUD": true,
	}
	// Map exchange acronym -> currency (one-shot lookup).
	exCurrency := map[string]string{}
	exResp, err := c.GET("/api/v3/stock-exchanges?page=1&page_size=50")
	if err == nil && exResp.StatusCode == 200 {
		if exs, ok := exResp.Body["exchanges"].([]interface{}); ok {
			for _, e := range exs {
				m, ok := e.(map[string]interface{})
				if !ok {
					continue
				}
				ac, _ := m["acronym"].(string)
				cur, _ := m["currency"].(string)
				if ac != "" && cur != "" {
					exCurrency[ac] = cur
				}
			}
		}
	}

	// Retry a few times — the seeder may still be populating listings.
	for attempt := 0; attempt < 10; attempt++ {
		resp, err := c.GET("/api/v3/securities/stocks?page=1&page_size=50")
		if err != nil {
			t.Fatalf("getFirstStockListingID: %v", err)
		}
		helpers.RequireStatus(t, resp, 200)

		stocks, ok := resp.Body["stocks"].([]interface{})
		if !ok || len(stocks) == 0 {
			time.Sleep(3 * time.Second)
			continue
		}

		for _, s := range stocks {
			stock, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			listing, ok := stock["listing"].(map[string]interface{})
			if !ok || listing == nil {
				continue
			}
			lid, ok := listing["id"].(float64)
			if !ok || lid == 0 {
				continue
			}
			priceStr, _ := listing["price"].(string)
			priceNum, _ := strconv.ParseFloat(priceStr, 64)
			if priceNum <= 0 {
				continue
			}
			acronym, _ := listing["exchange_acronym"].(string)
			if cur, ok := exCurrency[acronym]; ok && !supported[cur] {
				continue
			}
			stockID = uint64(stock["id"].(float64))
			listingID = uint64(lid)
			return
		}
		t.Logf("getFirstStockListingID: no tradeable stock yet, retrying (%d/10)...", attempt+1)
		time.Sleep(3 * time.Second)
	}
	t.Fatal("getFirstStockListingID: no stock with priced listing + supported-currency exchange found after 10 attempts")
	return
}

// getFirstFuturesID fetches futures and returns the first futures contract's ID.
func getFirstFuturesID(t *testing.T, c *client.APIClient) uint64 {
	t.Helper()
	resp, err := c.GET("/api/v3/securities/futures?page=1&page_size=1")
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
	resp, err := c.GET("/api/v3/securities/forex?page=1&page_size=1")
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
	resp, err := c.GET("/api/v3/securities/options?stock_id=" + helpers.FormatID(int(stockID)) + "&page_size=1")
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
