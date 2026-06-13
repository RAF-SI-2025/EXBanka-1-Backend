//go:build integration

package workflows

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
)

// SG-09: infrastructure failure on F1 (SAGA.pdf §SG-09a).
//
// With account-service down, the exercise saga's first step (reserve_strike →
// account-service) fails with a connection-level gRPC error (Unavailable), so
// the saga goes straight to Compensated with nothing prior to undo and the
// contract is left ACTIVE. Restoring the service and cleanly re-exercising the
// SAME contract proves no state was corrupted.
//
// Mechanism: `docker kill` drops the TCP connection so the in-flight RPC fails
// fast (Unavailable). A `docker pause` would instead stall the established
// connection and hang the call, so kill is the correct chaos primitive here.
//
// Container name defaults to the compose-generated account-service container and
// can be overridden with SG_ACCOUNT_CONTAINER.
func TestSG09_InfraDownOnF1_Compensates(t *testing.T) {
	sgEnabled(t)
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH — SG-09 orchestrates the account-service container")
	}
	container := os.Getenv("SG_ACCOUNT_CONTAINER")
	if container == "" {
		container = "exbanka-1-backend-account-service-1"
	}

	adminC := loginAsAdmin(t)
	contractID, buyerC, _ := sgSetupContract(t, adminC)

	// Safety net: whatever happens, make sure account-service is back before we
	// leave (so the rest of the suite / stack keeps working).
	defer func() {
		_ = exec.Command("docker", "start", container).Run()
		waitDepUp(t, buyerC, 90*time.Second)
	}()

	dockerMust(t, "kill", container)
	exResp, exErr := sgExercise(buyerC, contractID, nil)

	// Bring it back before asserting, so contract reads are reliable.
	dockerMust(t, "start", container)
	waitDepUp(t, buyerC, 90*time.Second)

	if exErr != nil {
		t.Fatalf("exercise: %v", exErr)
	}
	if exResp.StatusCode == 201 {
		t.Fatalf("SG-09 expected exercise to fail while account-service is down, got 201")
	}
	if st := contractStatus(t, buyerC, contractID); st != "ACTIVE" {
		t.Fatalf("SG-09 expected contract ACTIVE after infra-down compensation, got %q", st)
	}

	// Clean retry now that the service is restored.
	retry, err := sgExercise(buyerC, contractID, nil)
	if err != nil {
		t.Fatalf("clean retry after restore: %v", err)
	}
	if retry.StatusCode != 201 {
		t.Fatalf("SG-09 clean retry after restore expected 201, got %d body=%v", retry.StatusCode, retry.Body)
	}
}

func dockerMust(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v failed: %v\n%s", args, err, out)
	}
}

// waitDepUp polls a buyer read that requires account-service until it returns
// 200, so the suite never continues before a restarted dependency is reachable.
func waitDepUp(t *testing.T, c *client.APIClient, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := c.GET("/api/v3/me/accounts")
		if err == nil && resp.StatusCode == 200 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("dependency did not come back up within %s", timeout)
}

// waitStockUp polls a stock-service-backed read until it returns 200.
func waitStockUp(t *testing.T, c *client.APIClient, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := c.GET("/api/v3/me/otc/contracts")
		if err == nil && resp.StatusCode == 200 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("stock-service did not come back up within %s", timeout)
}

// contractStatusSafe is a non-fatal contract-status read (returns "" on any
// error), for polling a contract while the coordinator is restarting.
func contractStatusSafe(c *client.APIClient, contractID int) string {
	resp, err := c.GET("/api/v3/me/otc/contracts")
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	arr, _ := resp.Body["contracts"].([]interface{})
	for _, x := range arr {
		m, ok := x.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := m["id"].(float64); ok && int(id) == contractID {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

// SG-11: the coordinator (stock-service) is SIGKILLed mid-flight, then restarted
// (SAGA.pdf §SG-11). The saga-recovery reconciler reads the persisted log and
// drives the stranded saga to a terminal state. BOTH outcomes are valid:
// Completed (contract EXERCISED, forward-resumed) or Compensated (contract
// ACTIVE and cleanly re-exercisable) — provided the contract ends terminal with
// no hanging reservations. We crash inside settle_strike_buyer (after
// reserve_strike committed) via an injected delay.
func TestSG11_CoordinatorKilledMidFlight_Recovers(t *testing.T) {
	sgEnabled(t)
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH — SG-11 orchestrates the stock-service container")
	}
	stockC := os.Getenv("SG_STOCK_CONTAINER")
	if stockC == "" {
		stockC = "exbanka-1-backend-stock-service-1"
	}

	adminC := loginAsAdmin(t)
	contractID, buyerC, _ := sgSetupContract(t, adminC)

	// Always make sure the coordinator is back, whatever happens.
	defer func() {
		_ = exec.Command("docker", "start", stockC).Run()
		waitStockUp(t, buyerC, 150*time.Second)
	}()

	// Fire an exercise that stalls ~6s inside settle_strike_buyer so we can crash
	// the coordinator mid-saga.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sgExercise(buyerC, contractID, map[string]string{"X-Saga-Inject-Delay": "settle_strike_buyer:6000"})
	}()

	time.Sleep(2 * time.Second) // let the saga reach the delayed step
	dockerMust(t, "kill", stockC)
	dockerMust(t, "start", stockC)
	<-done // the in-flight exercise request died when the coordinator was killed

	waitStockUp(t, buyerC, 150*time.Second)

	// Recovery must drive the stranded saga to a clean terminal state.
	deadline := time.Now().Add(200 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = adminC.POST("/api/v3/admin/crons/stock-service/saga-recovery/trigger", map[string]interface{}{"force": true})
		switch contractStatusSafe(buyerC, contractID) {
		case "EXERCISED":
			return // recovery resumed forward to completion
		case "ACTIVE":
			// recovery rolled back — the contract must be cleanly re-exercisable
			if retry, err := sgExercise(buyerC, contractID, nil); err == nil && retry.StatusCode == 201 {
				return
			}
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("SG-11 saga did not reach a clean terminal state after coordinator restart")
}
