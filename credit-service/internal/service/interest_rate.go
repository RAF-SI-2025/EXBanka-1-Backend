package service

import "math"

// GetNominalInterestRate returns the nominal annual interest rate percentage for a loan type
func GetNominalInterestRate(loanType, interestType string) float64 {
	rates := map[string]map[string]float64{
		"cash":        {"fixed": 9.5, "variable": 7.5},
		"housing":     {"fixed": 3.5, "variable": 2.5},
		"auto":        {"fixed": 5.5, "variable": 4.0},
		"refinancing": {"fixed": 7.0, "variable": 5.5},
		"student":     {"fixed": 2.0, "variable": 1.5},
	}
	if r, ok := rates[loanType]; ok {
		if rate, ok := r[interestType]; ok {
			return rate
		}
	}
	return 8.0 // default
}

// CalculateEffectiveInterestRate computes EIR from nominal rate and compounding periods per year
func CalculateEffectiveInterestRate(nominalRate float64, periodsPerYear int) float64 {
	r := nominalRate / 100 / float64(periodsPerYear)
	eir := (math.Pow(1+r, float64(periodsPerYear)) - 1) * 100
	return math.Round(eir*10000) / 10000
}

// CalculateMonthlyInstallment uses annuity formula: P * r / (1 - (1+r)^-n)
func CalculateMonthlyInstallment(principal float64, annualRatePercent float64, months int) float64 {
	r := annualRatePercent / 12 / 100
	if r == 0 {
		return principal / float64(months)
	}
	factor := math.Pow(1+r, float64(months))
	payment := principal * r * factor / (factor - 1)
	return math.Round(payment*100) / 100
}
