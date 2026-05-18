package saga_test

import (
	"strings"
	"testing"

	"github.com/exbanka/contract/shared/saga"
)

func TestIdempotencyKey_DeterministicAndBounded(t *testing.T) {
	a := saga.IdempotencyKey("saga-123", saga.StepDebitBuyer)
	b := saga.IdempotencyKey("saga-123", saga.StepDebitBuyer)
	if a != b {
		t.Errorf("not deterministic: %s vs %s", a, b)
	}
	if len(a) > 100 {
		t.Errorf("idempotency key too long: %d", len(a))
	}
	if !strings.Contains(a, "saga-123") || !strings.Contains(a, "debit_buyer") {
		t.Errorf("expected key to contain saga id and step name: %s", a)
	}
}

func TestIdempotencyKey_DistinctPerStep(t *testing.T) {
	a := saga.IdempotencyKey("saga-1", saga.StepDebitBuyer)
	b := saga.IdempotencyKey("saga-1", saga.StepCreditSeller)
	if a == b {
		t.Error("different steps should produce different keys")
	}
}
