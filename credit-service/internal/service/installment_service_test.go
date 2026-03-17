package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateInstallmentSchedule(t *testing.T) {
	installments := CreateInstallmentSchedule(100000.0, 5.0, 12, "RSD", "2026-01-01")
	assert.Len(t, installments, 12, "should create 12 installments for 12-month loan")

	// All should be unpaid initially
	for _, inst := range installments {
		assert.Equal(t, "unpaid", inst.Status)
	}

	// Monthly amounts should be reasonable
	totalPayback := 0.0
	for _, inst := range installments {
		totalPayback += inst.Amount
	}
	assert.Greater(t, totalPayback, 100000.0, "total payback must exceed principal")
	assert.Less(t, totalPayback, 115000.0, "total payback should not be excessive")
}
