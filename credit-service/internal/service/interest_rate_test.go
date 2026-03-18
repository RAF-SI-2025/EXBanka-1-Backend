package service

import (
	"testing"

	"github.com/shopspring/decimal"
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
			rateF, _ := rate.Float64()
			assert.GreaterOrEqual(t, rateF, tt.minRate, "rate should be >= minRate")
			assert.LessOrEqual(t, rateF, tt.maxRate, "rate should be <= maxRate")
		})
	}
}

func TestCalculateEffectiveInterestRate(t *testing.T) {
	nominal := decimal.NewFromFloat(5.0)
	eir := CalculateEffectiveInterestRate(nominal, 12)
	nominalF, _ := nominal.Float64()
	eirF, _ := eir.Float64()
	assert.Greater(t, eirF, nominalF, "EIR must be greater than nominal")
	assert.Less(t, eirF, nominalF*2, "EIR should not be unreasonably high")
}

func TestCalculateMonthlyInstallment(t *testing.T) {
	amount := decimal.NewFromFloat(100000.0)
	annualRate := decimal.NewFromFloat(5.0)
	months := 12
	installment := CalculateMonthlyInstallment(amount, annualRate, months)
	installmentF, _ := installment.Float64()
	assert.Greater(t, installmentF, 8000.0)
	assert.Less(t, installmentF, 9000.0)
}
