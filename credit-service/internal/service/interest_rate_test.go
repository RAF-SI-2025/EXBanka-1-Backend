package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

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
