package saga_test

import (
	"context"
	"testing"

	"github.com/exbanka/contract/shared/saga"
)

func TestContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = saga.WithSagaID(ctx, "abc-123")
	ctx = saga.WithSagaStep(ctx, saga.StepDebitBuyer)
	ctx = saga.WithActingEmployeeID(ctx, 42)

	if got, ok := saga.SagaIDFromContext(ctx); !ok || got != "abc-123" {
		t.Errorf("SagaIDFromContext: got (%q, %v)", got, ok)
	}
	if got, ok := saga.SagaStepFromContext(ctx); !ok || got != saga.StepDebitBuyer {
		t.Errorf("SagaStepFromContext: got (%q, %v)", got, ok)
	}
	if got, ok := saga.ActingEmployeeIDFromContext(ctx); !ok || got != 42 {
		t.Errorf("ActingEmployeeIDFromContext: got (%d, %v)", got, ok)
	}
}

func TestContext_MissingValuesReturnZero(t *testing.T) {
	if _, ok := saga.SagaIDFromContext(context.Background()); ok {
		t.Error("expected ok=false on empty context")
	}
}
