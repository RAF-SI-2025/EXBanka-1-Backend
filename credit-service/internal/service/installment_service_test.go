package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestCreateInstallmentSchedule(t *testing.T) {
	amount := decimal.NewFromFloat(100000.0)
	annualRate := decimal.NewFromFloat(5.0)
	installments := CreateInstallmentSchedule(amount, annualRate, 12, "RSD", "2026-01-01")
	assert.Len(t, installments, 12, "should create 12 installments for 12-month loan")

	// All should be unpaid initially
	for _, inst := range installments {
		assert.Equal(t, "unpaid", inst.Status)
	}

	// Monthly amounts should be reasonable
	totalPayback := decimal.Zero
	for _, inst := range installments {
		totalPayback = totalPayback.Add(inst.Amount)
	}
	totalF, _ := totalPayback.Float64()
	assert.Greater(t, totalF, 100000.0, "total payback must exceed principal")
	assert.Less(t, totalF, 115000.0, "total payback should not be excessive")
}
