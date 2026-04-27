package saga

// IdempotencyKey produces a deterministic key for a single saga step.
// Callees use this key to deduplicate retried calls — calling twice with
// the same key returns the cached response without re-executing.
func IdempotencyKey(sagaID string, step StepKind) string {
	return sagaID + ":" + string(step)
}
