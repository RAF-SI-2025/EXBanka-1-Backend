//go:build integration
// +build integration

package workflows

import (
	"testing"
)

// TestActuaryLimit_EmployeeMeOrder_OwnerType is a regression scaffold
// for the Phase-3 actuary-limit bug discovered while implementing plan
// docs/superpowers/plans/2026-04-27-owner-type-schema.md.
//
// Bug shape
//
//	An employee with a low actuary single-trade limit (say 100 RSD) calls
//	POST /api/v3/me/orders. The api-gateway middleware OwnerIsBankIfEmployee
//	resolves owner=bank, acting_employee_id=<employee>. Stock-service must
//	enforce the actuary single-trade limit on acting_employee_id, NOT on
//	owner_id. Pre-fix behaviour rejected on owner_id (which is NIL for the
//	bank) and let oversized trades through; post-fix it consults the user-
//	service's employee_limits table via acting_employee_id.
//
// Expected behaviour
//
//	Employee places /me/orders with amount > MaxSingleTransaction → 403
//	with body.error.code == "forbidden" and "actuary" or "limit" in message.
//
// Status
//
//	Scaffold only — wiring an integration that creates an employee with
//	specific actuary limits requires an admin route to set
//	employee_limits.MaxSingleTransaction directly. The current admin
//	limits API surfaces single-transaction limits via PUT
//	/api/v3/employees/:id/limits, but the test plumbing for that flow
//	(setupAgentEmployee + a helper to PATCH limits) is not yet in the
//	shared helpers. Filed as a follow-up so the slot is reserved.
func TestActuaryLimit_EmployeeMeOrder_OwnerType(t *testing.T) {
	t.Parallel()
	t.Skip("requires test infrastructure for setting up actuary limits via PUT /api/v3/employees/:id/limits — implement when the limits-helper lands in helpers_test.go")
}
