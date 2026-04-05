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

	// Verify the annuity formula independently:
	// Monthly rate r = 5% / 12 = 0.0041666...
	// Payment = P * r / (1 - (1+r)^-n)
	// = 100000 * 0.0041666... / (1 - 1.0041666...^-12)
	// Expected ~8560.75 per month (total ~102729)
	expectedMonthly := CalculateMonthlyInstallment(amount, annualRate, 12)

	// All installments should have the same monthly amount (annuity formula)
	for i, inst := range installments {
		assert.True(t, inst.Amount.Equal(expectedMonthly),
			"installment %d: amount %s should equal calculated monthly payment %s", i, inst.Amount, expectedMonthly)
	}

	// All should be unpaid initially
	for i, inst := range installments {
		assert.Equal(t, "unpaid", inst.Status, "installment %d should be unpaid", i)
	}

	// Verify currency code is propagated to every installment
	for i, inst := range installments {
		assert.Equal(t, "RSD", inst.CurrencyCode, "installment %d should have currency RSD", i)
	}

	// Verify interest rate is propagated to every installment
	for i, inst := range installments {
		assert.True(t, inst.InterestRate.Equal(annualRate),
			"installment %d should carry the annual rate %s", i, annualRate)
	}

	// Verify due dates are spaced monthly starting from 2026-02-01
	for i, inst := range installments {
		expectedMonth := 2 + i // first due is Feb 2026
		expectedYear := 2026
		if expectedMonth > 12 {
			expectedMonth -= 12
			expectedYear++
		}
		assert.Equal(t, expectedYear, inst.ExpectedDate.Year(),
			"installment %d: year should be %d", i, expectedYear)
		assert.Equal(t, expectedMonth, int(inst.ExpectedDate.Month()),
			"installment %d: month should be %d", i, expectedMonth)
		assert.Equal(t, 1, inst.ExpectedDate.Day(),
			"installment %d: day should be 1", i)
	}

	// Verify total payback exceeds principal (interest > 0)
	totalPayback := decimal.Zero
	for _, inst := range installments {
		totalPayback = totalPayback.Add(inst.Amount)
	}
	assert.True(t, totalPayback.GreaterThan(amount),
		"total payback %s must exceed principal %s", totalPayback, amount)
	// For 5% annual on 100k over 12 months, total interest ~2729
	totalInterest := totalPayback.Sub(amount)
	interestF, _ := totalInterest.Float64()
	assert.InDelta(t, 2729.0, interestF, 100.0,
		"total interest should be approximately 2729 for 5%% annual on 100k/12mo")
}
