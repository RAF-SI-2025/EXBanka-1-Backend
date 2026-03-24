//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF5: Employee Limit Management ---

func TestLimits_GetEmployeeLimits(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/employees/1/limits")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLimits_SetEmployeeLimits(t *testing.T) {
	c := loginAsAdmin(t)

	createResp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Limit"),
		"last_name":     helpers.RandomName("Test"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         helpers.RandomEmail(),
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID := helpers.GetNumberField(t, createResp, "id")

	resp, err := c.PUT(fmt.Sprintf("/api/employees/%d/limits", int(empID)), map[string]interface{}{
		"max_loan_approval_amount": "500000.00",
		"max_single_transaction":   "100000.00",
		"max_daily_transaction":    "250000.00",
		"max_client_daily_limit":   "50000.00",
		"max_client_monthly_limit": "200000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Verify limits were set
	getResp, err := c.GET(fmt.Sprintf("/api/employees/%d/limits", int(empID)))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, getResp, 200)
}

func TestLimits_ApplyTemplate(t *testing.T) {
	c := loginAsAdmin(t)

	createResp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Template"),
		"last_name":     helpers.RandomName("Test"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         helpers.RandomEmail(),
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID := helpers.GetNumberField(t, createResp, "id")

	resp, err := c.POST(fmt.Sprintf("/api/employees/%d/limits/template", int(empID)), map[string]interface{}{
		"template_name": "BasicTeller",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLimits_ListTemplates(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/limits/templates")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestLimits_CreateTemplate(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/limits/templates", map[string]interface{}{
		"name":                     fmt.Sprintf("TestTemplate_%d", helpers.DateOfBirthUnix()),
		"description":              "Test template",
		"max_loan_approval_amount": "1000000.00",
		"max_single_transaction":   "500000.00",
		"max_daily_transaction":    "750000.00",
		"max_client_daily_limit":   "100000.00",
		"max_client_monthly_limit": "500000.00",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}
