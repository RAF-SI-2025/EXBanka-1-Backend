package saga_test

import (
	"strings"
	"testing"

	"github.com/exbanka/contract/shared/saga"
)

func TestMustStep_KnownKindReturnsItself(t *testing.T) {
	got := saga.MustStep(saga.StepReserveBuyerFunds)
	if got != saga.StepReserveBuyerFunds {
		t.Errorf("got %q, want %q", got, saga.StepReserveBuyerFunds)
	}
}

func TestMustStep_UnknownKindPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unknown StepKind")
		}
		if !strings.Contains(toString(r), "unknown StepKind") {
			t.Errorf("panic message: %v", r)
		}
	}()
	_ = saga.MustStep(saga.StepKind("totally_made_up"))
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return ""
}
