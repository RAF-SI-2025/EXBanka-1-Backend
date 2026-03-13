package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNominalInterestRate(t *testing.T) {
	tests := []struct {
		name         string
		loanType     string
		interestType string
		minRate      float64
		maxRate      float64
	}{
		{"cash fixed", "cash", "fixed", 5.0, 15.0},
		{"housing fixed", "housing", "fixed", 2.0, 8.0},
		{"auto fixed", "auto", "fixed", 3.0, 10.0},
		{"student variable", "student", "variable", 1.0, 5.0},
		{"refinancing fixed", "refinancing", "fixed", 3.0, 12.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := GetNominalInterestRate(tt.loanType, tt.interestType)
			assert.GreaterOrEqual(t, rate, tt.minRate, "rate should be >= minRate")
			assert.LessOrEqual(t, rate, tt.maxRate, "rate should be <= maxRate")
		})
	}
}

func TestCalculateEffectiveInterestRate(t *testing.T) {
	nominal := 5.0
	eir := CalculateEffectiveInterestRate(nominal, 12)
	assert.Greater(t, eir, nominal, "EIR must be greater than nominal")
	assert.Less(t, eir, nominal*2, "EIR should not be unreasonably high")
}

func TestCalculateMonthlyInstallment(t *testing.T) {
	amount := 100000.0
	annualRate := 5.0
	months := 12
	installment := CalculateMonthlyInstallment(amount, annualRate, months)
	assert.Greater(t, installment, 8000.0)
	assert.Less(t, installment, 9000.0)
}
