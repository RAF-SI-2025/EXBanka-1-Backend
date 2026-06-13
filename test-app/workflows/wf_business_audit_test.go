//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_BusinessAuditLog_LimitChange verifies the business audit log
// end-to-end (SP2): an admin sets an employee's limit, and the action surfaces
// in GET /api/v3/admin/audit/business-actions (action=limit.set) with the
// target employee. The publish→Kafka→consumer→DB hop is async, so the audit
// endpoint is polled until the entry appears.
func TestWF_BusinessAuditLog_LimitChange(t *testing.T) {
	adminC := loginAsAdmin(t)

	createResp, err := adminC.POST("/api/v3/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Audit"),
		"last_name":     helpers.RandomName("Target"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         helpers.RandomEmail(),
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID := int(helpers.GetNumberField(t, createResp, "id"))

	setResp, err := adminC.PUT(fmt.Sprintf("/api/v3/employees/%d/limits", empID), map[string]interface{}{
		"max_loan_approval_amount": "500000.00",
		"max_single_transaction":   "100000.00",
		"max_daily_transaction":    "250000.00",
		"max_client_daily_limit":   "50000.00",
		"max_client_monthly_limit": "200000.00",
	})
	if err != nil {
		t.Fatalf("set limits: %v", err)
	}
	helpers.RequireStatus(t, setResp, 200)

	// Poll the audit endpoint until the limit.set entry for this employee shows.
	target := fmt.Sprintf("%d", empID)
	deadline := time.Now().Add(20 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		auditResp, err := adminC.GET("/api/v3/admin/audit/business-actions?action=limit.set&target_type=employee&page_size=200")
		if err != nil {
			t.Fatalf("get audit: %v", err)
		}
		if auditResp.StatusCode == 404 {
			t.Skip("business-actions audit endpoint not deployed — skipping")
		}
		helpers.RequireStatus(t, auditResp, 200)
		entries, _ := helpers.RequireField(t, auditResp, "entries").([]interface{})
		for _, raw := range entries {
			e, _ := raw.(map[string]interface{})
			if e["action"] == "limit.set" && e["target_id"] == target && e["target_type"] == "employee" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !found {
		t.Fatalf("limit.set audit entry for employee %d not found within timeout", empID)
	}
}
