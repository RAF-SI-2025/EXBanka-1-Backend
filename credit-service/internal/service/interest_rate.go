package service

import "github.com/shopspring/decimal"

// GetNominalInterestRate returns the nominal annual interest rate percentage for a loan type
func GetNominalInterestRate(loanType, interestType string) decimal.Decimal {
	rates := map[string]map[string]decimal.Decimal{
		"cash":        {"fixed": decimal.NewFromFloat(9.5), "variable": decimal.NewFromFloat(7.5)},
		"housing":     {"fixed": decimal.NewFromFloat(3.5), "variable": decimal.NewFromFloat(2.5)},
		"auto":        {"fixed": decimal.NewFromFloat(5.5), "variable": decimal.NewFromFloat(4.0)},
		"refinancing": {"fixed": decimal.NewFromFloat(7.0), "variable": decimal.NewFromFloat(5.5)},
		"student":     {"fixed": decimal.NewFromFloat(2.0), "variable": decimal.NewFromFloat(1.5)},
	}
	if m, ok := rates[loanType]; ok {
		if r, ok := m[interestType]; ok {
			return r
		}
	}
	return decimal.NewFromFloat(8.0)
}

// CalculateEffectiveInterestRate computes EIR from nominal rate and compounding periods per year
func CalculateEffectiveInterestRate(nominalRate decimal.Decimal, periodsPerYear int) decimal.Decimal {
	// (1 + r/12)^12 - 1 using decimal arithmetic
	r := nominalRate.Div(decimal.NewFromInt(100)).Div(decimal.NewFromInt(12))
	one := decimal.NewFromInt(1)
	return one.Add(r).Pow(decimal.NewFromInt(int64(periodsPerYear))).Sub(one).Mul(decimal.NewFromInt(100))
}

// CalculateMonthlyInstallment uses annuity formula: P * r / (1 - (1+r)^-n)
func CalculateMonthlyInstallment(principal, annualRatePercent decimal.Decimal, months int) decimal.Decimal {
	if months <= 0 {
		return decimal.Zero
	}
	r := annualRatePercent.Div(decimal.NewFromInt(100)).Div(decimal.NewFromInt(12))
	if r.IsZero() {
		return principal.Div(decimal.NewFromInt(int64(months)))
	}
	n := decimal.NewFromInt(int64(months))
	one := decimal.NewFromInt(1)
	// P * r / (1 - (1+r)^-n)
	numerator := principal.Mul(r)
	denominator := one.Sub(one.Add(r).Pow(n.Neg()))
	return numerator.Div(denominator)
}
