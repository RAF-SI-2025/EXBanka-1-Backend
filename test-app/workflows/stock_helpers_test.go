//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// setupAgentEmployee creates an employee with EmployeeAgent role, activates them,
// and returns the employee ID and an authenticated client.
func setupAgentEmployee(t *testing.T, adminC *client.APIClient) (empID int, agentC *client.APIClient, email string) {
	t.Helper()
	email = helpers.RandomEmail()
	password := helpers.RandomPassword()

	createResp, err := adminC.POST("/api/employees", map[string]interface{}{
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

	createResp, err := adminC.POST("/api/employees", map[string]interface{}{
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
	resp, err := c.GET("/api/securities/stocks?page=1&page_size=1")
	if err != nil {
		t.Fatalf("getFirstStockListingID: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	stocks, ok := resp.Body["stocks"].([]interface{})
	if !ok || len(stocks) == 0 {
		t.Fatal("getFirstStockListingID: no stocks found")
	}
	stock := stocks[0].(map[string]interface{})
	stockID = uint64(stock["id"].(float64))

	listing := stock["listing"].(map[string]interface{})
	listingID = uint64(listing["id"].(float64))
	return
}
