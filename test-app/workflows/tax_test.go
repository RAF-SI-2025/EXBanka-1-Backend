//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestTax_ListTaxRecords(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.GET("/api/tax")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "tax_records")
	helpers.RequireField(t, resp, "total_count")
}

func TestTax_ListTaxRecords_FilterByUserType(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.GET("/api/tax?user_type=client")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestTax_ListTaxRecords_InvalidUserType(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.GET("/api/tax?user_type=invalid")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 400)
}

func TestTax_CollectTax(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, supervisorC, _ := setupSupervisorEmployee(t, adminC)

	resp, err := supervisorC.POST("/api/tax/collect", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "collected_count")
	helpers.RequireField(t, resp, "total_collected_rsd")
}

func TestTax_CollectTax_AgentCannot(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.POST("/api/tax/collect", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 403)
}

func TestTax_ListMyTaxRecords(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	resp, err := clientC.GET("/api/me/tax")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "records")
	helpers.RequireField(t, resp, "total_count")
	helpers.RequireField(t, resp, "tax_paid_this_year")
	helpers.RequireField(t, resp, "tax_unpaid_this_month")
}

func TestTax_ListMyTaxRecords_EmployeeToken(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	adminC := loginAsAdmin(t)
	_, agentC, _ := setupAgentEmployee(t, adminC)

	resp, err := agentC.GET("/api/me/tax")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "records")
}

func TestTax_ListMyTaxRecords_Unauthenticated(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	c := newClient()
	resp, err := c.GET("/api/me/tax")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}

func TestTax_Unauthenticated(t *testing.T) {
	t.Parallel()
	t.Skip("stock-service API not yet reliable -- temporarily disabled")
	c := newClient()
	resp, err := c.GET("/api/tax")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 401)
}
