//go:build integration

package workflows

import (
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// SG-* additional SAGA.pdf exercise-saga scenarios, complementing saga_sg_test.go.
//
// Same harness and gating: each fault scenario forces a named phase of the
// exercise saga to fail via the X-Saga-Force-Fail header (honored only when
// stock-service is built with -tags sagafaults), then asserts the spec's
// all-or-nothing guarantee end-to-end: the contract is left ACTIVE (never
// consumed) AND a subsequent clean exercise of the SAME contract succeeds —
// only possible if the seller's shares, both parties' funds, and the contract's
// status were fully restored (SAGA.pdf invariants I1/I2/I3/I6).
//
// Phase → our exercise-saga step mapping (otc_exercise_saga.go):
//   F1 reserve buyer funds      → reserve_strike
//   F3 transfer funds           → settle_strike_buyer + credit_strike_seller
//   F4 transfer ownership       → consume_seller_holding + upsert_buyer_holding
//   F5 finalize / consume contract → mark_contract_exercised

// sgForceFailExpectActiveThenRetry forces the named step to fail, asserts the
// contract is left ACTIVE (full compensation), then proves restoration by a
// clean retry succeeding. Shared by SG-03/04/06.
func sgForceFailExpectActiveThenRetry(t *testing.T, step string) {
	t.Helper()
	adminC := loginAsAdmin(t)
	contractID, buyerC, _ := sgSetupContract(t, adminC)

	failResp, err := sgExercise(buyerC, contractID, map[string]string{"X-Saga-Force-Fail": step})
	if err != nil {
		t.Fatalf("exercise(force-fail %s): %v", step, err)
	}
	if failResp.StatusCode == 201 {
		t.Skip("forced failure did not take effect — stock-service not built with -tags sagafaults")
	}
	if st := contractStatus(t, buyerC, contractID); st != "ACTIVE" {
		t.Fatalf("expected contract ACTIVE after compensation of %s, got %q", step, st)
	}
	// Full restoration (funds + shares released) is proven by a clean retry of
	// the SAME contract succeeding.
	retry, err := sgExercise(buyerC, contractID, nil)
	if err != nil {
		t.Fatalf("clean retry after %s: %v", step, err)
	}
	if retry.StatusCode != 201 {
		t.Fatalf("clean retry after %s expected 201 (restored state), got %d body=%v", step, retry.StatusCode, retry.Body)
	}
}

// SG-02c: a contract that is no longer "active" (already exercised) is rejected
// pre-saga, with no side effects.
func TestSG02c_AlreadyExercisedRejected(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	contractID, buyerC, _ := sgSetupContract(t, adminC)

	first, err := sgExercise(buyerC, contractID, nil)
	if err != nil {
		t.Fatalf("first exercise: %v", err)
	}
	if first.StatusCode != 201 {
		t.Fatalf("SG-02c first (clean) exercise expected 201, got %d body=%v", first.StatusCode, first.Body)
	}
	if st := contractStatus(t, buyerC, contractID); st != "EXERCISED" {
		t.Fatalf("SG-02c expected contract EXERCISED after a clean exercise, got %q", st)
	}

	second, err := sgExercise(buyerC, contractID, nil)
	if err != nil {
		t.Fatalf("second exercise: %v", err)
	}
	if second.StatusCode < 400 || second.StatusCode >= 500 {
		t.Fatalf("SG-02c expected 4xx exercising an already-exercised contract, got %d body=%v", second.StatusCode, second.Body)
	}
}

// SG-03: F1 (reserve buyer funds) fails — the saga compensates with no prior
// step to undo. Modeled via a forced failure of reserve_strike.
func TestSG03_ForceFailReserveStrike_CompensatesAndRetrySucceeds(t *testing.T) {
	sgEnabled(t)
	sgForceFailExpectActiveThenRetry(t, "reserve_strike")
}

// SG-04: the share leg fails (analogue of the spec's F2 "seller has insufficient
// shares") — the money steps must roll back. Modeled via a forced failure of
// consume_seller_holding.
func TestSG04_ForceFailConsumeSellerHolding_MoneyRollsBack(t *testing.T) {
	sgEnabled(t)
	sgForceFailExpectActiveThenRetry(t, "consume_seller_holding")
}

// SG-06: F4 (transfer ownership) fails at the buyer-credit step — funds return
// to the buyer, the seller's debit reverses, shares return to the seller.
// Modeled via a forced failure of upsert_buyer_holding.
func TestSG06_ForceFailUpsertBuyerHolding_FullCompensation(t *testing.T) {
	sgEnabled(t)
	sgForceFailExpectActiveThenRetry(t, "upsert_buyer_holding")
}

// sgAccountBalance returns the ledger balance of a specific account the client
// owns, via GET /api/v3/me/accounts/{id}. Reading the exact bound account (not
// accounts[0]) is required — the buyer has more than one account.
func sgAccountBalance(t *testing.T, c *client.APIClient, acctID uint64) float64 {
	t.Helper()
	resp, err := c.GET("/api/v3/me/accounts/" + helpers.FormatID(int(acctID)))
	if err != nil {
		t.Fatalf("me/accounts/%d: %v", acctID, err)
	}
	helpers.RequireStatus(t, resp, 200)
	return parseJSONBalance(t, resp.Body, "balance")
}

// SG-08: a compensator fails once, then the recovery reconciler heals it.
//
// We force F3 (credit_strike_seller) to fail AND force the settle_strike_buyer
// compensator to fail once. The exercise saga has no inline compensator retry,
// so the buyer's strike debit is left un-refunded (stuck "compensating") — but
// the contract is NEVER consumed (stays ACTIVE, invariant I6). The saga-recovery
// reconciler (60s tick, 30s stuck threshold) must finish the rollback and fully
// refund the buyer; the recovery context carries no fault header, so the second
// compensation attempt succeeds. This proves the spec's "compensators repeat
// until success" guarantee end-to-end (here via the async reconciler).
func TestSG08_CompensatorFailsOnceThenRecovers(t *testing.T) {
	sgEnabled(t)
	adminC := loginAsAdmin(t)
	contractID, buyerC, buyerAcctID := sgSetupContract(t, adminC)

	b0 := sgAccountBalance(t, buyerC, buyerAcctID)

	failResp, err := sgExercise(buyerC, contractID, map[string]string{
		"X-Saga-Force-Fail":            "credit_strike_seller",
		"X-Saga-Compensate-Fail":       "settle_strike_buyer",
		"X-Saga-Compensate-Fail-Times": "1",
	})
	if err != nil {
		t.Fatalf("exercise(force-fail + compensate-fail): %v", err)
	}
	if failResp.StatusCode == 201 {
		t.Skip("forced failure did not take effect — stock-service not built with -tags sagafaults")
	}

	// I6: the contract is not consumed while compensation is incomplete.
	if st := contractStatus(t, buyerC, contractID); st != "ACTIVE" {
		t.Fatalf("SG-08 expected contract ACTIVE, got %q", st)
	}

	// The settle compensator failed once → the buyer's strike debit is stuck.
	bStuck := sgAccountBalance(t, buyerC, buyerAcctID)
	if bStuck >= b0 {
		t.Fatalf("SG-08 expected the bound account balance to drop by the stuck debit (b0=%.4f, stuck=%.4f)", b0, bStuck)
	}

	// The recovery reconciler must finish the rollback and refund the buyer.
	// Poll, nudging the cron each tick to speed past the 60s schedule.
	deadline := time.Now().Add(160 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = adminC.POST("/api/v3/admin/crons/stock-service/saga-recovery/trigger", map[string]interface{}{"force": true})
		if sgAccountBalance(t, buyerC, buyerAcctID) >= b0 {
			return // fully refunded to the pre-exercise balance — SG-08 satisfied
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("SG-08 bound account balance not restored by the recovery reconciler within timeout (b0=%.4f, last=%.4f)", b0, sgAccountBalance(t, buyerC, buyerAcctID))
}
