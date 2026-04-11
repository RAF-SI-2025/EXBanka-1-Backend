//go:build integration

package workflows

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_LoanFullLifecycle exercises the complete loan lifecycle:
//
//	client + RSD account → submit housing loan request (2M, 60 months, fixed) →
//	admin approves → balance increased by ~2M → 60 installments created →
//	first installment amount validated against annuity formula with 5% tolerance.
func TestWF_LoanFullLifecycle(t *testing.T) {
	adminC := loginAsAdmin(t)

	// Step 1: Create client with funded RSD account
	clientID, accountNum, clientC, _ := setupActivatedClient(t, adminC)
	t.Logf("WF-4: account=%s", accountNum)

	// Step 2: Record balance before loan disbursement
	balBefore := getAccountBalance(t, adminC, accountNum)

	// Step 3: Submit housing loan request (2M RSD, 60 months, fixed interest)
	const loanAmount = 2000000.0
	const months = 60
	loanID := createLoanAndApprove(t, adminC, clientC, "housing", loanAmount, accountNum, months, clientID)
	t.Logf("WF-4: loan approved id=%d", loanID)

	// Step 5: Assert balance increased by approximately 2M (loan disbursed)
	balAfter := getAccountBalance(t, adminC, accountNum)
	balIncrease := balAfter - balBefore
	tolerance := loanAmount * 0.05
	if balIncrease < loanAmount-tolerance || balIncrease > loanAmount+tolerance {
		t.Errorf("WF-4: balance increase %.2f, expected ~%.2f (±5%%)", balIncrease, loanAmount)
	}
	t.Logf("WF-4: balance before=%.2f, after=%.2f, increase=%.2f", balBefore, balAfter, balIncrease)

	// Step 6: Get installments
	installmentsResp, err := adminC.GET(fmt.Sprintf("/api/v1/loans/%d/installments", loanID))
	if err != nil {
		t.Fatalf("WF-4: get installments: %v", err)
	}
	helpers.RequireStatus(t, installmentsResp, 200)

	var installments []interface{}
	if arr, ok := installmentsResp.Body["installments"]; ok {
		raw, _ := json.Marshal(arr)
		json.Unmarshal(raw, &installments)
	}

	// Step 7: Assert 60 installments created
	if len(installments) != months {
		t.Fatalf("WF-4: expected %d installments, got %d", months, len(installments))
	}
	t.Logf("WF-4: %d installments created", len(installments))

	// Step 8: Validate first installment amount against annuity formula.
	// Standard annuity formula: PMT = P * r / (1 - (1+r)^-n)
	// where P = principal, r = monthly interest rate, n = number of months.
	// Housing fixed rate is typically around 5-10% annually; we use a reasonable
	// range-based check with 5% tolerance since we don't know the exact rate.
	if len(installments) > 0 {
		firstInstallment, ok := installments[0].(map[string]interface{})
		if !ok {
			t.Fatalf("WF-4: first installment is not a map")
		}

		// Extract the installment amount (may be float64 or string)
		var installmentAmount float64
		if amtVal, ok := firstInstallment["amount"]; ok {
			switch v := amtVal.(type) {
			case float64:
				installmentAmount = v
			case string:
				installmentAmount, _ = strconv.ParseFloat(v, 64)
			}
		}

		if installmentAmount <= 0 {
			t.Fatalf("WF-4: first installment amount <= 0: %f", installmentAmount)
		}

		// Validate with annuity formula assuming annual rate between 3% and 15%
		// (wide range to be safe; exact rate depends on service config).
		validateInstallmentInRange(t, loanAmount, months, installmentAmount, 0.03, 0.15)
	}

	t.Logf("WF-4: PASS — housing loan %d disbursed, %d installments created", loanID, len(installments))
}

// validateInstallmentInRange checks that the actual installment amount falls within
// the range of annuity payments calculated from minAnnualRate to maxAnnualRate.
// Applies 5% tolerance on both ends.
func validateInstallmentInRange(t *testing.T, principal float64, months int, actual float64, minAnnualRate, maxAnnualRate float64) {
	t.Helper()

	annuity := func(annualRate float64) float64 {
		r := annualRate / 12.0
		n := float64(months)
		if r < 1e-9 {
			return principal / n
		}
		return principal * r / (1 - math.Pow(1+r, -n))
	}

	low := annuity(minAnnualRate) * 0.95
	high := annuity(maxAnnualRate) * 1.05

	if actual < low || actual > high {
		t.Errorf("WF-4: installment amount %.2f outside expected range [%.2f, %.2f] "+
			"(annual rates %.1f%%–%.1f%%, 5%% tolerance)",
			actual, low, high, minAnnualRate*100, maxAnnualRate*100)
	} else {
		t.Logf("WF-4: installment amount %.2f within range [%.2f, %.2f]", actual, low, high)
	}
}
