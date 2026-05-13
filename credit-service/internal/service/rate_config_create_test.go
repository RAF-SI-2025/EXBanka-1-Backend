package service

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/credit-service/internal/model"
)

func TestCreateTier_NegativeVariableBase(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	err := svc.CreateTier(&model.InterestRateTier{
		AmountFrom: decimal.Zero, AmountTo: decimal.NewFromInt(1000),
		FixedRate: decimal.NewFromFloat(5), VariableBase: decimal.NewFromFloat(-1),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidRateConfig))
	assert.Contains(t, err.Error(), "variable_base must not be negative")
}

func TestCreateTier_NegativeAmountFrom(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	err := svc.CreateTier(&model.InterestRateTier{
		AmountFrom: decimal.NewFromInt(-1), AmountTo: decimal.NewFromInt(1000),
		FixedRate: decimal.NewFromFloat(5), VariableBase: decimal.NewFromFloat(5),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount_from must not be negative")
}

func TestCreateTier_NegativeAmountTo(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	err := svc.CreateTier(&model.InterestRateTier{
		AmountFrom: decimal.Zero, AmountTo: decimal.NewFromInt(-1),
		FixedRate: decimal.NewFromFloat(5), VariableBase: decimal.NewFromFloat(5),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount_to must not be negative")
}

func TestCreateTier_AmountToLessOrEqualAmountFrom(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	err := svc.CreateTier(&model.InterestRateTier{
		AmountFrom: decimal.NewFromInt(1000), AmountTo: decimal.NewFromInt(500),
		FixedRate: decimal.NewFromFloat(5), VariableBase: decimal.NewFromFloat(5),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount_to must be greater than amount_from")
}

func TestCreateTier_OpenTopBoundary(t *testing.T) {
	// AmountTo=0 is the "no upper bound" sentinel and must be allowed.
	svc, _ := buildRateConfigSvc(t)
	tier := &model.InterestRateTier{
		AmountFrom:   decimal.NewFromInt(50_000_000),
		AmountTo:     decimal.Zero,
		FixedRate:    decimal.NewFromFloat(4),
		VariableBase: decimal.NewFromFloat(4),
	}
	require.NoError(t, svc.CreateTier(tier))
	assert.NotZero(t, tier.ID)
	assert.True(t, tier.Active, "newly created tier must be marked active")
}

func TestUpdateTier_NegativeVariableBase(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)
	patch := &model.InterestRateTier{
		ID: existing.ID, AmountFrom: existing.AmountFrom, AmountTo: existing.AmountTo,
		FixedRate: existing.FixedRate, VariableBase: decimal.NewFromFloat(-1),
	}
	err := svc.UpdateTier(patch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "variable_base must not be negative")
}

func TestUpdateTier_NegativeAmountFrom(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)
	patch := &model.InterestRateTier{
		ID: existing.ID, AmountFrom: decimal.NewFromInt(-1), AmountTo: existing.AmountTo,
		FixedRate: existing.FixedRate, VariableBase: existing.VariableBase,
	}
	err := svc.UpdateTier(patch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount_from must not be negative")
}

func TestUpdateTier_NegativeAmountTo(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)
	patch := &model.InterestRateTier{
		ID: existing.ID, AmountFrom: existing.AmountFrom, AmountTo: decimal.NewFromInt(-1),
		FixedRate: existing.FixedRate, VariableBase: existing.VariableBase,
	}
	err := svc.UpdateTier(patch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount_to must not be negative")
}

func TestGetNominalRate_VariableBranch(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	rate, err := svc.GetNominalRate("housing", "variable", decimal.NewFromInt(800_000))
	require.NoError(t, err)
	assert.True(t, rate.IsPositive())
}

func TestGetNominalRate_BankMarginNotFound(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	_, err := svc.GetNominalRate("nonexistent_loan_type", "fixed", decimal.NewFromInt(50_000))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBankMarginNotFound))
}

func TestGetNominalRateComponents_VariableBranch(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	base, margin, total, err := svc.GetNominalRateComponents("auto", "variable", decimal.NewFromInt(2_500_000))
	require.NoError(t, err)
	assert.True(t, base.IsPositive())
	assert.True(t, margin.IsPositive())
	assert.True(t, total.Equal(base.Add(margin)))
}

func TestGetNominalRateComponents_BankMarginNotFound(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	_, _, _, err := svc.GetNominalRateComponents("nope", "fixed", decimal.NewFromInt(50_000))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBankMarginNotFound))
}

func TestCreateInstallmentSchedule_FallsBackOnInvalidDate(t *testing.T) {
	// Malformed date string must trigger the time.Now() fallback branch
	// without panicking. Months count must still be exact.
	insts := CreateInstallmentSchedule(decimal.NewFromInt(10_000), decimal.NewFromFloat(8), 6, "RSD", "not-a-date")
	require.Len(t, insts, 6)
	for _, i := range insts {
		assert.Equal(t, "RSD", i.CurrencyCode)
		assert.Equal(t, "unpaid", i.Status)
	}
}
