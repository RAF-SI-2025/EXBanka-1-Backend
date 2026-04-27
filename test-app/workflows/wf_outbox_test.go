//go:build integration

package workflows

import "testing"

// TestOutbox_KillBeforeDrainerPublishes — kill stock-service after the
// outbox row commits but before the drainer publishes; restart; verify
// the row eventually publishes.
//
// Spec: §21.2 — saga-published Kafka events route through
// contract/shared/outbox. The Enqueue(tx, topic, payload, saga_id) write
// goes into the same DB transaction as the saga step's business action.
// A per-service OutboxDrainer goroutine reads pending rows, publishes
// to Kafka, marks published_at. Crash between commit and publish is
// safe: the drainer picks up unpublished rows on restart.
//
// What this test should verify (when implemented):
//   1. Configure stock-service with a controllable drainer (e.g.
//      env-toggle to disable the goroutine).
//   2. Run an OTC accept saga to completion. Assert outbox row exists
//      with published_at = NULL.
//   3. Re-enable the drainer / restart the service.
//   4. Poll Kafka for the otc.contract-created event; assert it
//      eventually arrives.
//   5. Assert the outbox row's published_at is now non-NULL.
//
// This is currently a t.Skip placeholder because the test-app harness
// can't yet kill / restart individual docker-compose services mid-test.
// Filling this in is tracked in the future-ideas backlog under
// "docker-compose service restart helper for resilience tests".
func TestOutbox_KillBeforeDrainerPublishes(t *testing.T) {
	t.Skip("requires docker-compose mid-test service restart helper (future-ideas backlog item)")
}
