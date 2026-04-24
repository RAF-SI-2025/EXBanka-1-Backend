//go:build integration

package workflows

import "testing"

// TestWF_StockFill_CommissionFailure_TradeStillCompletes is a Phase-2 regression
// guard: if the commission-credit step of the fill saga fails (e.g., bank
// account unavailable), the user's trade must still complete — the commission
// is non-critical and must not block settlement. Instead it should remain in
// saga_logs as a pending step for the recovery reconciler to retry later.
//
// This test is SKIPPED today because, like wf_stock_fill_failure_test, it
// requires fault injection — specifically forcing the commission-credit call
// to account-service to fail while letting the rest of the fill saga proceed
// normally.
//
// The existing recovery design (Task 17) handles this: commission-credit is
// its own saga step, and failure is logged, not fatal. But we cannot exercise
// that path from the outside without a fault injection hook.
//
// TODO(phase-3): add a per-saga-step fault injection flag (e.g., via a new
// admin RPC on stock-service: ForceNextSagaStepFail(step="credit_commission"))
// and wire this test to it. Then assert:
//   1. order reaches is_done=true
//   2. holding is credited, user account debited correctly
//   3. bank RSD balance did NOT rise by commission yet
//   4. saga_logs contains an entry with status=pending/compensating for
//      credit_commission
//   5. after waiting for the recovery reconciler's interval, bank RSD
//      balance DOES rise, and the saga_log entry is status=completed.
func TestWF_StockFill_CommissionFailure_TradeStillCompletes(t *testing.T) {
	t.Skip("fault-injection infrastructure missing: need a per-step fault flag on stock-service saga — see TODO above")
}
