package service

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// referenceAnnuity computes the expected monthly installment using the standard
// annuity formula in float64 for cross-validation:
//
//	A = P * r * (1+r)^n / ((1+r)^n - 1)
//
// where r = annual_rate_percent / 100 / 12, n = months.
func referenceAnnuity(principal, annualRatePercent float64, months int) float64 {
	r := annualRatePercent / 100.0 / 12.0
	if r == 0 {
		return principal / float64(months)
	}
	n := float64(months)
	pow := math.Pow(1+r, n)
	return principal * r * pow / (pow - 1)
}

// TestInstallmentFormula_Housing1_5M verifies the installment amount for a housing
// loan of 1,500,000 RSD using the seeded rate configuration.
//
// Amount 1.5M falls in tier 1M-2M (amount_from<=1.5M, amount_to=2M > 1.5M):
//   base fixed rate = 5.75%
// Housing margin: 1.50%
// Nominal annual rate: 7.25%
func TestInstallmentFormula_Housing1_5M(t *testing.T) {
	annualRate := 7.25 // 5.75% base + 1.50% margin
	principal := 1_500_000.0
	months := 240 // housing typical

	expected := referenceAnnuity(principal, annualRate, months)
	actual := CalculateMonthlyInstallment(
		decimal.NewFromFloat(principal),
		decimal.NewFromFloat(annualRate),
		months,
	)
	actualF, _ := actual.Float64()

	assert.InDelta(t, expected, actualF, 0.01,
		"monthly installment for 1.5M RSD housing @ 7.25%% over 240 months should match annuity formula")

	// Sanity: for 1.5M at 7.25% over 20 years, monthly payment should be around 11,850
	assert.InDelta(t, 11850, actualF, 200,
		"monthly payment should be approximately 11,850 RSD")

	// Verify total payback exceeds principal
	totalPayback := actualF * float64(months)
	assert.Greater(t, totalPayback, principal, "total payback must exceed principal")
}

// TestInstallmentFormula_Cash300k verifies the installment amount for a cash
// loan of 300,000 RSD using the seeded rate configuration.
//
// Tier 0-500k: base fixed rate = 6.25%
// Cash margin: 1.75%
// Nominal annual rate: 8.00%
func TestInstallmentFormula_Cash300k(t *testing.T) {
	annualRate := 8.00 // 6.25% base + 1.75% margin
	principal := 300_000.0
	months := 36

	expected := referenceAnnuity(principal, annualRate, months)
	actual := CalculateMonthlyInstallment(
		decimal.NewFromFloat(principal),
		decimal.NewFromFloat(annualRate),
		months,
	)
	actualF, _ := actual.Float64()

	assert.InDelta(t, expected, actualF, 0.01,
		"monthly installment for 300k RSD cash @ 8%% over 36 months should match annuity formula")

	// Sanity: for 300k at 8% over 3 years, monthly payment should be around 9,400
	assert.InDelta(t, 9400, actualF, 200,
		"monthly payment should be approximately 9,400 RSD")
}

// TestInstallmentFormula_ZeroRate verifies that with 0% interest the monthly
// payment is simply principal / months.
func TestInstallmentFormula_ZeroRate(t *testing.T) {
	principal := decimal.NewFromInt(120000)
	months := 12
	payment := CalculateMonthlyInstallment(principal, decimal.Zero, months)
	expected := decimal.NewFromInt(10000)
	assert.True(t, payment.Equal(expected),
		"with 0%% rate, payment should be 120000/12=10000; got %s", payment)
}

// TestInstallmentFormula_ZeroMonths verifies that 0 months returns zero.
func TestInstallmentFormula_ZeroMonths(t *testing.T) {
	payment := CalculateMonthlyInstallment(decimal.NewFromInt(100000), decimal.NewFromFloat(5.0), 0)
	assert.True(t, payment.IsZero(), "0 months should return zero payment")
}

// TestEffectiveInterestRate_MatchesCompounding verifies the EIR formula:
// EIR = (1 + r/12)^12 - 1
func TestEffectiveInterestRate_MatchesCompounding(t *testing.T) {
	nominalPercent := 7.25
	r := nominalPercent / 100.0 / 12.0
	expectedEIR := (math.Pow(1+r, 12) - 1) * 100.0

	actual := CalculateEffectiveInterestRate(decimal.NewFromFloat(nominalPercent), 12)
	actualF, _ := actual.Float64()

	assert.InDelta(t, expectedEIR, actualF, 0.001,
		"EIR for 7.25%% nominal should be ~%f; got %f", expectedEIR, actualF)
	assert.Greater(t, actualF, nominalPercent, "EIR must exceed nominal rate")
}

// TestInstallmentFormula_EndToEnd_WithRateConfig creates a full pipeline:
// seed tiers + margins, look up rate, compute installment, verify against spec.
func TestInstallmentFormula_EndToEnd_WithRateConfig(t *testing.T) {
	// Use rate_config_service_test helpers to build the service with seeded data
	svc, _ := buildRateConfigSvc(t)

	// Housing loan, 1.5M RSD, fixed interest
	amount := decimal.NewFromInt(1_500_000)
	baseRate, bankMargin, nominalRate, err := svc.GetNominalRateComponents("housing", "fixed", amount)
	require.NoError(t, err)

	// Tier 1M-2M: fixed=5.75, housing margin=1.50 => nominal=7.25
	assert.True(t, baseRate.Equal(decimal.NewFromFloat(5.75)), "base rate: got %s", baseRate)
	assert.True(t, bankMargin.Equal(decimal.NewFromFloat(1.50)), "margin: got %s", bankMargin)
	assert.True(t, nominalRate.Equal(decimal.NewFromFloat(7.25)), "nominal: got %s", nominalRate)

	months := 180
	payment := CalculateMonthlyInstallment(amount, nominalRate, months)
	paymentF, _ := payment.Float64()

	expectedPayment := referenceAnnuity(1_500_000.0, 7.25, 180)
	assert.InDelta(t, expectedPayment, paymentF, 0.01,
		"end-to-end installment should match reference formula")

	// Verify EIR
	eir := CalculateEffectiveInterestRate(nominalRate, 12)
	eirF, _ := eir.Float64()
	nominalF, _ := nominalRate.Float64()
	assert.Greater(t, eirF, nominalF, "EIR should exceed nominal rate")
}
