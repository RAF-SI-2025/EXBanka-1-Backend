//go:build integration

package workflows

// TestSP2_EmployeeLimitReplica_ApprovalGate verifies the SP-2 end-to-end path:
//
//  1. Set the admin employee's MaxLoanApprovalAmount to a low value via
//     PUT /api/v3/employees/{id}/limits — user-service publishes
//     EmployeeLimitsUpdatedMessage to user.employee-limits-updated.
//  2. credit-service's EmployeeLimitReplicaConsumer (group
//     credit-service-employee-limit-replica) processes the event and
//     updates the EmployeeLimitReplica row in credit_db.
//  3. A client submits a loan-request whose amount EXCEEDS the limit.
//  4. When the admin tries to approve it, credit-service reads the replica
//     (MaxLoanApprovalAmount < requested amount) and returns
//     ErrAmountExceedsApprovalLimit (gRPC FailedPrecondition → HTTP 409).
//  5. At the end of the test the admin's limit is RESTORED to a high value
//     so subsequent tests that use admin approval are not broken.
//
// NOTE: this test is NOT run against a live stack here (Docker is not
// running in this environment). It compiles under -tags=integration and CI
// executes it against the full stack. The EmployeeLimitReplica gate
// enforcement is additionally covered by credit-service unit tests in
// credit-service/internal/service/ and
// credit-service/internal/repository/employee_limit_replica_repository_test.go.

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

func TestSP2_EmployeeLimitReplica_ApprovalGate(t *testing.T) {
	// Do NOT run in parallel — this test mutates the shared admin's limit
	// and restores it at the end; parallel execution could interleave with
	// other tests that approve loans via the same admin account.

	adminC := loginAsAdmin(t)

	// Step 1 — resolve the admin employee ID.
	// The seeded admin always has ID 1, but we look it up defensively.
	adminEmpID := resolveAdminEmployeeID(t, adminC)
	t.Logf("admin employee id: %d", adminEmpID)

	// Step 2 — lower the admin's MaxLoanApprovalAmount below the loan amount we
	// will request (500 000 RSD). We set 100 000 RSD so the gate definitely fires.
	const lowLimit = "100000.00"
	const highValue = "999999999.00"
	const loanAmount = 500000

	setLimitResp, err := adminC.PUT(fmt.Sprintf("/api/v3/employees/%d/limits", adminEmpID), map[string]interface{}{
		"max_loan_approval_amount": lowLimit,
		"max_single_transaction":   highValue,
		"max_daily_transaction":    highValue,
		"max_client_daily_limit":   highValue,
		"max_client_monthly_limit": highValue,
	})
	if err != nil {
		t.Fatalf("set admin limits: %v", err)
	}
	helpers.RequireStatus(t, setLimitResp, 200)
	t.Logf("admin MaxLoanApprovalAmount set to %s", lowLimit)

	// Always restore the admin's limit so other tests are not affected.
	t.Cleanup(func() {
		restoreResp, rerr := adminC.PUT(fmt.Sprintf("/api/v3/employees/%d/limits", adminEmpID), map[string]interface{}{
			"max_loan_approval_amount": highValue,
			"max_single_transaction":   highValue,
			"max_daily_transaction":    highValue,
			"max_client_daily_limit":   highValue,
			"max_client_monthly_limit": highValue,
		})
		if rerr != nil {
			t.Logf("WARN: failed to restore admin limits: %v", rerr)
			return
		}
		if restoreResp.StatusCode != 200 {
			t.Logf("WARN: restore admin limits returned %d", restoreResp.StatusCode)
		} else {
			t.Logf("admin limits restored to %s", highValue)
		}
	})

	// Step 3 — set up a client + funded RSD account.
	clientID, accountNumber, clientC, _ := setupActivatedClient(t, adminC)

	meResp, err := clientC.GET("/api/v3/me")
	if err != nil {
		t.Fatalf("GET /api/v3/me: %v", err)
	}
	helpers.RequireStatus(t, meResp, 200)
	meClientID := int(helpers.GetNumberField(t, meResp, "id"))

	_ = clientID // referenced via setupActivatedClient return

	// Step 4 — client submits a loan request for an amount ABOVE the admin's limit.
	loanReqResp, err := clientC.POST("/api/v3/me/loan-requests", map[string]interface{}{
		"client_id":        meClientID,
		"loan_type":        "cash",
		"interest_type":    "fixed",
		"amount":           loanAmount,
		"currency_code":    "RSD",
		"repayment_period": 12,
		"account_number":   accountNumber,
	})
	if err != nil {
		t.Fatalf("create loan request: %v", err)
	}
	helpers.RequireStatus(t, loanReqResp, 201)
	loanReqID := int(helpers.GetNumberField(t, loanReqResp, "id"))
	t.Logf("loan request id: %d (amount: %d RSD, limit: %s)", loanReqID, loanAmount, lowLimit)

	// Step 5 — poll the approve endpoint for up to 10 s, waiting for the
	// EmployeeLimitReplica to be populated by the Kafka consumer
	// (user.employee-limits-updated → credit-service-employee-limit-replica).
	//
	// Expected steady-state: HTTP 409 — FailedPrecondition / business_rule_violation
	// ("amount exceeds employee approval limit").
	//
	// IMPORTANT: once the approve returns 200 the loan request is consumed and
	// we cannot retry. If we receive a 200 on the first attempt it means the
	// Kafka event arrived between the PUT and the approval call (race condition),
	// so we fail immediately with a clear message.
	approveURL := fmt.Sprintf("/api/v3/loan-requests/%d/approve", loanReqID)
	deadline := time.Now().Add(10 * time.Second)

	var lastStatus int
	for time.Now().Before(deadline) {
		approveResp, aerr := adminC.POST(approveURL, nil)
		if aerr != nil {
			t.Fatalf("approve loan request: %v", aerr)
		}
		lastStatus = approveResp.StatusCode

		switch lastStatus {
		case 409:
			// Gate fired — replica propagated correctly.
			t.Logf("approval correctly rejected with 409 (business_rule_violation): "+
				"amount %d > MaxLoanApprovalAmount %s", loanAmount, lowLimit)
			return
		case 200:
			// Loan was approved before the replica propagated.
			// This is a test-timing issue: the limit update and the approval raced.
			// Fail clearly so the CI log shows what happened.
			t.Fatalf("loan request %d was APPROVED (200) — the approval gate did not fire. "+
				"Possible cause: the EmployeeLimitReplica propagated AFTER the approve call "+
				"(unlikely <100 ms), OR the replica was already populated from a previous test "+
				"run with a higher value. Check credit-service consumer lag. "+
				"MaxLoanApprovalAmount set to %s, loan amount %d RSD.",
				loanReqID, lowLimit, loanAmount)
		default:
			// Any other non-success status (404, 500 …) is unexpected.
			t.Fatalf("unexpected approve status %d (body: %s)", lastStatus, string(approveResp.RawBody))
		}
	}

	t.Fatalf("approval gate not triggered within 10 s: last status=%d — "+
		"EmployeeLimitReplica may not have propagated from user.employee-limits-updated "+
		"(consumer group: credit-service-employee-limit-replica)",
		lastStatus)
}

// resolveAdminEmployeeID returns the employee ID for the seeded admin account.
// It tries the well-known ID=1 first; on a miss it scans the list.
func resolveAdminEmployeeID(t *testing.T, adminC *client.APIClient) int {
	t.Helper()
	resp, err := adminC.GET("/api/v3/employees/1")
	if err != nil {
		t.Fatalf("resolveAdminEmployeeID GET /employees/1: %v", err)
	}
	if resp.StatusCode == 200 {
		return int(helpers.GetNumberField(t, resp, "id"))
	}
	// Fallback: scan employee list and pick the first entry.
	listResp, err := adminC.GET("/api/v3/employees")
	if err != nil {
		t.Fatalf("resolveAdminEmployeeID GET /employees: %v", err)
	}
	helpers.RequireStatus(t, listResp, 200)
	employees, ok := listResp.Body["employees"].([]interface{})
	if !ok || len(employees) == 0 {
		t.Fatal("resolveAdminEmployeeID: empty employee list")
	}
	emp, ok := employees[0].(map[string]interface{})
	if !ok {
		t.Fatal("resolveAdminEmployeeID: unexpected employee shape")
	}
	idF, ok := emp["id"].(float64)
	if !ok {
		t.Fatal("resolveAdminEmployeeID: employee id is not a number")
	}
	return int(idF)
}
