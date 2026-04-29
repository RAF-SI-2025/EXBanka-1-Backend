//go:build integration

package workflows

import "testing"

// TestWF_StockFill_AccountServiceFailure_NoDivergence is a Phase-2 regression
// guard for bug #4: when account-service returns an error mid-fill (e.g., the
// PartialSettle RPC fails transiently), the saga must either:
//
//   - roll back cleanly via compensation (no double-debit, reservation
//     released), OR
//   - eventually complete via saga recovery (no orphaned pending-step rows),
//
// in either case leaving no ledger divergence.
//
// This test is SKIPPED today because fault injection requires one of:
//
//   - a gRPC interceptor registered at startup on stock-service → account-service
//     (not currently wired up), OR
//   - an admin endpoint on account-service to force PartialSettle to fail
//     (does not exist), OR
//   - killing account-service mid-fill and restarting it (doable via
//     docker-compose but fragile in CI).
//
// The plan's Task 22 step 3 describes the docker-compose kill-and-restart
// variant as a manual smoke test; a proper automated version will require a
// --force-fail-partial-settle admin flag on account-service.
//
// TODO(phase-3): add POST /api/v3/admin/fault-inject/account-partial-settle
// endpoint (guarded by INTEGRATION_FAULT_INJECTION=1 env gate), wire it into
// this test, and remove the t.Skip call below.
func TestWF_StockFill_AccountServiceFailure_NoDivergence(t *testing.T) {
	t.Skip("fault-injection infrastructure missing: need an admin endpoint on account-service to force PartialSettle to fail — see TODO above")
}
