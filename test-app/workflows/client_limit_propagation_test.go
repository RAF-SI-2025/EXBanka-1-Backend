//go:build integration

package workflows

// TestSP5_ClientLimitPropagation_AccountCap verifies the SP-5 end-to-end path:
//
//  1. Set up a client with a funded RSD account.
//  2. PUT /api/v3/clients/{clientID}/limits with a low daily_limit (1500.00) —
//     client-service persists the limit, publishes client.limits-updated
//     (enriched: DailyLimit, MonthlyLimit, TransferLimit + monotonic Version).
//  3. account-service's ClientLimitConsumer (group account-service-client-limit)
//     receives the event, upserts a ClientLimitPolicy row, and calls
//     ApplyClientLimitPolicy which writes the DailyLimit/MonthlyLimit to every
//     non-bank account owned by the client.
//  4. POLL GET /api/v3/accounts?account_number=... for up to 10 s; assert that
//     the account's daily_limit field converges to 1500 (the policy value).
//
// NOTE: This test was NOT run against a live stack in the authoring environment
// (Docker is not running). It is compile-verified via:
//
//	cd test-app && go vet -tags=integration ./workflows/
//
// CI executes it against the full stack.
// SP-5 account-service unit coverage lives in:
//   - account-service/internal/consumer/client_limit_consumer_test.go
//   - account-service/internal/repository/client_limit_policy_repository_test.go
//   - account-service/internal/service/account_service_test.go

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

func TestSP5_ClientLimitPropagation_AccountCap(t *testing.T) {
	// Do NOT run in parallel — mutates a freshly-created client's limits; no
	// shared state but serialising keeps CI output readable.

	adminC := loginAsAdmin(t)

	// Step 1 — create a client with a funded RSD account.
	clientID, accountNumber, _, _ := setupActivatedClient(t, adminC)
	t.Logf("client id=%d  account_number=%s", clientID, accountNumber)

	// Step 2 — set a low daily_limit (1500.00) on the client.
	// The seeded admin has unlimited MaxClientDailyLimit so 1500 is well within bounds.
	const wantDailyLimit = 1500.0
	putResp, err := adminC.PUT(fmt.Sprintf("/api/v3/clients/%d/limits", clientID), map[string]interface{}{
		"daily_limit":    "1500.00",
		"monthly_limit":  "50000.00",
		"transfer_limit": "10000.00",
	})
	if err != nil {
		t.Fatalf("PUT client limits: %v", err)
	}
	helpers.RequireStatus(t, putResp, 200)
	t.Log("client limits set: daily_limit=1500.00")

	// Step 3 — poll the account until its daily_limit matches the policy or
	// timeout. Propagation is async: client-service → Kafka (client.limits-updated)
	// → account-service ClientLimitConsumer → UpdateAccountLimits per account.
	//
	// Tolerance: ±0.01 (decimal rounding). Poll every 500 ms for up to 10 s.
	const tolerance = 0.01
	deadline := time.Now().Add(10 * time.Second)
	var lastDailyLimit float64
	for time.Now().Before(deadline) {
		resp, gerr := adminC.GET("/api/v3/accounts?account_number=" + accountNumber)
		if gerr != nil {
			t.Fatalf("GET account: %v", gerr)
		}
		helpers.RequireStatus(t, resp, 200)

		accts, ok := resp.Body["accounts"].([]interface{})
		if !ok || len(accts) == 0 {
			t.Fatalf("no account found for number %s", accountNumber)
		}
		m, ok := accts[0].(map[string]interface{})
		if !ok {
			t.Fatalf("accounts[0] has unexpected shape: %T", accts[0])
		}

		lastDailyLimit = parseJSONBalance(t, m, "daily_limit")
		diff := lastDailyLimit - wantDailyLimit
		if diff < -tolerance || diff > tolerance {
			t.Logf("daily_limit=%.4f (want %.4f) — waiting for propagation...", lastDailyLimit, wantDailyLimit)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Policy propagated successfully.
		t.Logf("SP-5 propagation confirmed: account %s daily_limit=%.4f (policy=%.4f)",
			accountNumber, lastDailyLimit, wantDailyLimit)
		return
	}

	t.Fatalf("SP-5: account daily_limit did not converge to %.4f within 10 s — "+
		"last observed value: %.4f  (account_number=%s, client_id=%d). "+
		"Check account-service ClientLimitConsumer (group account-service-client-limit) "+
		"and client.limits-updated Kafka topic.",
		wantDailyLimit, lastDailyLimit, accountNumber, clientID)
}
