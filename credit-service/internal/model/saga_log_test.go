package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestSagaLog_DefaultValues(t *testing.T) {
	l := SagaLog{
		SagaID:        "abc",
		LoanID:        42,
		StepNumber:    1,
		StepName:      "debit_bank",
		AccountNumber: "111000000",
		Amount:        decimal.NewFromInt(100),
	}
	if l.Status != "" {
		t.Fatalf("Status should default to empty (set by GORM default), got %q", l.Status)
	}
	if l.TableName() != "credit_saga_logs" {
		t.Fatalf("expected table name credit_saga_logs, got %q", l.TableName())
	}
}
