//go:build integration

package workflows

import "testing"

// TestIdempotency_DuplicateDebitDoesNotDoubleSpend — same idempotency key
// on two debit calls produces one ledger entry + one cached response.
//
// Spec: §21.2 — every saga-callee gRPC method accepts a string
// idempotency_key. Callees enforce the contract via a per-service
// idempotency_records table + repository.Run[T] wrapper. Retried saga
// steps return the cached response without re-executing.
//
// What this test should verify (when implemented):
//   1. Activate a client with a known account.
//   2. Call account-service.UpdateBalance directly (gRPC) twice with
//      identical (idempotency_key, payload).
//   3. Assert the ledger contains exactly ONE entry for that key.
//   4. Assert both responses match byte-for-byte.
//   5. (Bonus) Call a third time with the same key but DIFFERENT amount;
//      assert it returns the cached response, NOT a re-execution.
//
// This is currently a t.Skip placeholder because direct gRPC calls from
// test-app require new client wiring (test-app communicates via the
// REST gateway today, not directly with backend services). Filling
// this in is tracked in the future-ideas backlog under "test-app
// gRPC client harness".
func TestIdempotency_DuplicateDebitDoesNotDoubleSpend(t *testing.T) {
	t.Parallel()
	t.Skip("requires direct gRPC call to account-service from test-app — implement when test infra supports it")
}
