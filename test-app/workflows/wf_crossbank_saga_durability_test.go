//go:build integration
// +build integration

package workflows_test

import (
	"testing"
)

// TestCrossbankAccept_KillMidSaga_RecoversCleanly is a placeholder for an
// end-to-end durability test that proves the saga reconciler recovers a
// stock-service crash mid-saga.
//
// Scenario to implement once the test infrastructure exists:
//
//  1. Initiate a crossbank accept against the peer-bank stub.
//  2. Force the peer to delay/timeout on the transfer-ownership phase.
//  3. Kill stock-service while the saga ledger row is in `pending`.
//  4. Restart stock-service.
//  5. Wait for the recovery cron to reconcile.
//  6. Assert ledger rows reach a terminal state (`completed` or
//     `compensated`, never `pending`) and Kafka publishes a single
//     committed-or-rolled-back event.
//
// Blocked by: docker-compose helper to restart a single service mid-test.
// See future-ideas backlog (Spec F).
func TestCrossbankAccept_KillMidSaga_RecoversCleanly(t *testing.T) {
	t.Skip("requires docker-compose mid-test service restart helper")
}
