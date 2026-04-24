//go:build integration

package workflows

import "testing"

// TestWF_StopLimit_ExpiryReleasesReservation is a Phase-2 regression guard for
// the stop-limit refund path: a stop-limit order whose trigger never fires
// must eventually release its reservation. The two paths are:
//
//   - user cancels it manually → handled by the reservation test (cancel path)
//   - it expires or times out → handled here (passive release path)
//
// Today there is no admin endpoint to fast-forward time, no "expire all
// untriggered stops" admin RPC, and no sub-day expiry on stop-limit orders
// in the current stock-service implementation, so we cannot deterministically
// force the release inside a test run.
//
// SKIPPED with TODO below until one of:
//
//   - POST /api/v1/admin/stock/expire-stale-orders is added and invokes the
//     reservation-release path directly, OR
//   - simulator time advancement is exposed via a new admin endpoint (e.g.,
//     POST /api/v1/admin/sim/advance-hours?hours=25), OR
//   - stop-limit orders gain an explicit expires_at config that tests can
//     set short enough to observe expiry within a test timeout.
//
// TODO(phase-3): wire this up once one of the above lands. The assertion is:
//
//  1. place stop-limit buy with a stop_value so high it will never trigger
//  2. assert reserved_balance rose
//  3. trigger expiry (admin endpoint / time advance / wait)
//  4. assert reserved_balance back to 0 AND balance unchanged AND no
//     corresponding OrderTransaction row
func TestWF_StopLimit_ExpiryReleasesReservation(t *testing.T) {
	t.Skip("expiry infrastructure missing: no admin expire-stale-orders endpoint and no sub-day stop-limit expiry — see TODO above")
}
