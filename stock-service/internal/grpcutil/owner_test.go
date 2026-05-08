package grpcutil

import (
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOwnerFromLegacy_Client(t *testing.T) {
	tp, id := OwnerFromLegacy(42, "client")
	if tp != model.OwnerClient {
		t.Errorf("expected OwnerClient, got %v", tp)
	}
	if id == nil || *id != 42 {
		t.Errorf("expected id=42, got %v", id)
	}
}

func TestOwnerFromLegacy_Bank(t *testing.T) {
	tp, id := OwnerFromLegacy(0, "bank")
	if tp != model.OwnerBank {
		t.Errorf("expected OwnerBank, got %v", tp)
	}
	if id != nil {
		t.Errorf("expected nil id for bank, got %v", id)
	}
}

func TestActingEmployeeIDFromUint64_Zero(t *testing.T) {
	if got := ActingEmployeeIDFromUint64(0); got != nil {
		t.Errorf("expected nil for 0, got %v", got)
	}
}

func TestActingEmployeeIDFromUint64_NonZero(t *testing.T) {
	got := ActingEmployeeIDFromUint64(7)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if *got != 7 {
		t.Errorf("expected 7, got %d", *got)
	}
}

func TestActingEmployeeIDToUint64_Nil(t *testing.T) {
	if got := ActingEmployeeIDToUint64(nil); got != 0 {
		t.Errorf("expected 0 for nil, got %d", got)
	}
}

func TestActingEmployeeIDToUint64_Value(t *testing.T) {
	v := uint64(99)
	if got := ActingEmployeeIDToUint64(&v); got != 99 {
		t.Errorf("expected 99, got %d", got)
	}
}
