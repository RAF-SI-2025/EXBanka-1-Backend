package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// --- UpdateTier --------------------------------------------------------------

func TestUpdateTier_PersistsNewRates(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)

	patch := &model.InterestRateTier{
		ID:           existing.ID,
		AmountFrom:   existing.AmountFrom,
		AmountTo:     existing.AmountTo,
		FixedRate:    decimal.NewFromFloat(9.99),
		VariableBase: decimal.NewFromFloat(8.88),
	}
	require.NoError(t, svc.UpdateTier(patch))

	var reloaded model.InterestRateTier
	require.NoError(t, db.First(&reloaded, existing.ID).Error)
	assert.True(t, reloaded.FixedRate.Equal(decimal.NewFromFloat(9.99)))
	assert.True(t, reloaded.VariableBase.Equal(decimal.NewFromFloat(8.88)))
}

func TestUpdateTier_NotFound(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	patch := &model.InterestRateTier{
		ID:           99999,
		AmountFrom:   decimal.Zero,
		AmountTo:     decimal.NewFromInt(100000),
		FixedRate:    decimal.NewFromFloat(5),
		VariableBase: decimal.NewFromFloat(5),
	}
	err := svc.UpdateTier(patch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUpdateTier_NegativeFixedRateRejected(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)
	patch := &model.InterestRateTier{
		ID:           existing.ID,
		AmountFrom:   existing.AmountFrom,
		AmountTo:     existing.AmountTo,
		FixedRate:    decimal.NewFromFloat(-1),
		VariableBase: existing.VariableBase,
	}
	err := svc.UpdateTier(patch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed_rate must not be negative")
}

func TestUpdateTier_AmountToLessOrEqualAmountFromRejected(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)
	patch := &model.InterestRateTier{
		ID:           existing.ID,
		AmountFrom:   decimal.NewFromInt(1000),
		AmountTo:     decimal.NewFromInt(500),
		FixedRate:    decimal.NewFromFloat(5),
		VariableBase: decimal.NewFromFloat(5),
	}
	err := svc.UpdateTier(patch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount_to must be greater than amount_from")
}

// --- DeleteTier --------------------------------------------------------------

func TestDeleteTier_Removes(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var existing model.InterestRateTier
	require.NoError(t, db.First(&existing).Error)

	require.NoError(t, svc.DeleteTier(existing.ID))

	tiers, err := svc.ListTiers()
	require.NoError(t, err)
	assert.Len(t, tiers, 6, "after deleting one of seven seeded tiers, six remain")
}

func TestDeleteTier_NotFound(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	err := svc.DeleteTier(9999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- UpdateMargin negative path ----------------------------------------------

func TestUpdateMargin_NegativeRejected(t *testing.T) {
	svc, db := buildRateConfigSvc(t)
	var m model.BankMargin
	require.NoError(t, db.First(&m).Error)
	m.Margin = decimal.NewFromFloat(-1)

	err := svc.UpdateMargin(&m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "margin must not be negative")
}

func TestUpdateMargin_NotFound(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	err := svc.UpdateMargin(&model.BankMargin{ID: 9999, Margin: decimal.NewFromFloat(2.0)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- ApplyVariableRateUpdate -------------------------------------------------

func newRateConfigFullDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newRateConfigTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Loan{}, &model.Installment{}))
	return db
}

func TestApplyVariableRateUpdate_TierNotFound(t *testing.T) {
	db := newRateConfigFullDB(t)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installRepo := repository.NewInstallmentRepository(db)
	svc := NewRateConfigService(tierRepo, marginRepo, db)
	require.NoError(t, svc.SeedDefaults())

	count, err := svc.ApplyVariableRateUpdate(99999, loanRepo, installRepo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Equal(t, 0, count)
}

func TestApplyVariableRateUpdate_NoMatchingLoans(t *testing.T) {
	db := newRateConfigFullDB(t)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installRepo := repository.NewInstallmentRepository(db)
	svc := NewRateConfigService(tierRepo, marginRepo, db)
	require.NoError(t, svc.SeedDefaults())

	var t1 model.InterestRateTier
	require.NoError(t, db.First(&t1).Error)

	count, err := svc.ApplyVariableRateUpdate(t1.ID, loanRepo, installRepo)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestApplyVariableRateUpdate_PropagatesToVariableLoans(t *testing.T) {
	db := newRateConfigFullDB(t)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installRepo := repository.NewInstallmentRepository(db)
	svc := NewRateConfigService(tierRepo, marginRepo, db)
	require.NoError(t, svc.SeedDefaults())

	// Find the 0-500k tier and update its base
	var tier model.InterestRateTier
	require.NoError(t, db.Where("amount_from = ?", decimal.NewFromInt(0)).First(&tier).Error)
	tier.VariableBase = decimal.NewFromFloat(7.50)
	require.NoError(t, db.Save(&tier).Error)

	// Seed an active variable-rate loan in that range with unpaid installments
	loan := &model.Loan{
		LoanNumber:          "LN-VAR-1",
		LoanType:            "cash",
		AccountNumber:       "ACC-VAR-1",
		Amount:              decimal.NewFromInt(100000),
		RepaymentPeriod:     12,
		NominalInterestRate: decimal.NewFromFloat(8),
		CurrencyCode:        "RSD",
		Status:              "active",
		InterestType:        "variable",
		BaseRate:            decimal.NewFromFloat(6.25),
		BankMargin:          decimal.NewFromFloat(1.75),
		CurrentRate:         decimal.NewFromFloat(8),
		RemainingDebt:       decimal.NewFromInt(100000),
		ClientID:            1,
	}
	require.NoError(t, db.Create(loan).Error)
	for i := 0; i < 6; i++ {
		require.NoError(t, db.Create(&model.Installment{
			LoanID: loan.ID, Amount: decimal.NewFromInt(8000),
			InterestRate: decimal.NewFromFloat(8), CurrencyCode: "RSD",
			Status: "unpaid",
		}).Error)
	}

	// Seed an active *fixed* loan in same range — should be skipped
	fixed := &model.Loan{
		LoanNumber:          "LN-FIX-1",
		LoanType:            "cash",
		AccountNumber:       "ACC-FIX-1",
		Amount:              decimal.NewFromInt(200000),
		CurrencyCode:        "RSD",
		Status:              "active",
		InterestType:        "fixed",
		NominalInterestRate: decimal.NewFromFloat(8),
		BaseRate:            decimal.NewFromFloat(6.25),
		BankMargin:          decimal.NewFromFloat(1.75),
		CurrentRate:         decimal.NewFromFloat(8),
		RemainingDebt:       decimal.NewFromInt(200000),
		ClientID:            2,
	}
	require.NoError(t, db.Create(fixed).Error)

	count, err := svc.ApplyVariableRateUpdate(tier.ID, loanRepo, installRepo)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "only the variable loan should be updated")

	// Verify the variable loan was updated with new rate (7.50 + 1.75 = 9.25)
	var refreshed model.Loan
	require.NoError(t, db.First(&refreshed, loan.ID).Error)
	assert.True(t, refreshed.NominalInterestRate.Equal(decimal.NewFromFloat(9.25)),
		"variable loan nominal rate should become 7.50+1.75=9.25; got %s", refreshed.NominalInterestRate)

	// Verify the fixed loan is unchanged
	var fixedRefresh model.Loan
	require.NoError(t, db.First(&fixedRefresh, fixed.ID).Error)
	assert.True(t, fixedRefresh.NominalInterestRate.Equal(decimal.NewFromFloat(8)),
		"fixed loan should remain unchanged; got %s", fixedRefresh.NominalInterestRate)
}

func TestApplyVariableRateUpdate_NoUnpaidInstallmentsSkipped(t *testing.T) {
	db := newRateConfigFullDB(t)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installRepo := repository.NewInstallmentRepository(db)
	svc := NewRateConfigService(tierRepo, marginRepo, db)
	require.NoError(t, svc.SeedDefaults())

	var tier model.InterestRateTier
	require.NoError(t, db.Where("amount_from = ?", decimal.NewFromInt(0)).First(&tier).Error)

	// Variable loan with all installments paid
	loan := &model.Loan{
		LoanNumber:    "LN-PAID",
		LoanType:      "cash",
		AccountNumber: "ACC-PAID",
		Amount:        decimal.NewFromInt(100000),
		CurrencyCode:  "RSD",
		Status:        "active",
		InterestType:  "variable",
		BaseRate:      decimal.NewFromFloat(6.25),
		BankMargin:    decimal.NewFromFloat(1.75),
		CurrentRate:   decimal.NewFromFloat(8),
		RemainingDebt: decimal.NewFromInt(100000),
		ClientID:      1,
	}
	require.NoError(t, db.Create(loan).Error)

	count, err := svc.ApplyVariableRateUpdate(tier.ID, loanRepo, installRepo)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "loan with zero unpaid installments must be skipped")
}
