package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

func newRateConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.InterestRateTier{}, &model.BankMargin{}))
	return db
}

func buildRateConfigSvc(t *testing.T) (*RateConfigService, *gorm.DB) {
	t.Helper()
	db := newRateConfigTestDB(t)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)
	svc := NewRateConfigService(tierRepo, marginRepo, db)
	require.NoError(t, svc.SeedDefaults())
	return svc, db
}

// --- ListTiers ----------------------------------------------------------------

func TestListTiers_ReturnsAllSeededTiers(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	tiers, err := svc.ListTiers()
	require.NoError(t, err)
	assert.Len(t, tiers, 7, "SeedDefaults should create exactly 7 tiers")

	// Verify ordering by amount_from ascending
	for i := 1; i < len(tiers); i++ {
		assert.True(t, tiers[i].AmountFrom.GreaterThanOrEqual(tiers[i-1].AmountFrom),
			"tiers should be ordered by amount_from ascending")
	}
}

// --- CreateTier ---------------------------------------------------------------

func TestCreateTier_Persisted(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	tier := &model.InterestRateTier{
		AmountFrom:   decimal.NewFromInt(50_000_000),
		AmountTo:     decimal.Zero, // unlimited
		FixedRate:    decimal.NewFromFloat(4.00),
		VariableBase: decimal.NewFromFloat(3.75),
	}
	err := svc.CreateTier(tier)
	require.NoError(t, err)
	assert.NotZero(t, tier.ID, "tier should be assigned an ID")
	assert.True(t, tier.Active, "newly created tier should be active")

	// Verify via list
	tiers, err := svc.ListTiers()
	require.NoError(t, err)
	assert.Len(t, tiers, 8, "should now have 8 tiers (7 seeded + 1 new)")
}

func TestCreateTier_NegativeRateRejected(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	tier := &model.InterestRateTier{
		AmountFrom:   decimal.NewFromInt(0),
		AmountTo:     decimal.NewFromInt(100000),
		FixedRate:    decimal.NewFromFloat(-1.0),
		VariableBase: decimal.NewFromFloat(5.0),
	}
	err := svc.CreateTier(tier)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed_rate must not be negative")
}

// --- ListMargins --------------------------------------------------------------

func TestListMargins_ReturnsAllSeededMargins(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	margins, err := svc.ListMargins()
	require.NoError(t, err)
	assert.Len(t, margins, 5, "SeedDefaults should create margins for 5 loan types")

	// Verify all expected loan types are present
	types := make(map[string]bool)
	for _, m := range margins {
		types[m.LoanType] = true
	}
	for _, lt := range []string{"cash", "housing", "auto", "refinancing", "student"} {
		assert.True(t, types[lt], "margin for loan type %q should be present", lt)
	}
}

// --- UpdateMargin -------------------------------------------------------------

func TestUpdateMargin_ChangesValue(t *testing.T) {
	svc, db := buildRateConfigSvc(t)

	// Find the housing margin
	var margin model.BankMargin
	require.NoError(t, db.Where("loan_type = ?", "housing").First(&margin).Error)

	originalMargin := margin.Margin
	newMarginValue := decimal.NewFromFloat(2.25)
	margin.Margin = newMarginValue

	err := svc.UpdateMargin(&margin)
	require.NoError(t, err)
	assert.True(t, margin.Margin.Equal(newMarginValue), "margin should be updated to 2.25")
	assert.False(t, margin.Margin.Equal(originalMargin), "margin should differ from original")

	// Verify the nominal rate is affected
	nominalRate, err := svc.GetNominalRate("housing", "fixed", decimal.NewFromInt(1500000))
	require.NoError(t, err)
	// Tier for 1M-2M: fixed=5.75, new margin=2.25 => 8.00
	expected := decimal.NewFromFloat(8.00)
	assert.True(t, nominalRate.Equal(expected),
		"nominal rate should be 5.75 + 2.25 = 8.00; got %s", nominalRate)
}

// --- GetNominalRate / GetNominalRateComponents --------------------------------

func TestGetNominalRate_CashTierAndMargin(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	// Cash 300k: tier 0-500k => fixed=6.25; margin for cash=1.75 => 8.00
	rate, err := svc.GetNominalRate("cash", "fixed", decimal.NewFromInt(300000))
	require.NoError(t, err)
	expected := decimal.NewFromFloat(8.00)
	assert.True(t, rate.Equal(expected), "expected 6.25+1.75=8.00; got %s", rate)
}

func TestGetNominalRateComponents_HousingTier(t *testing.T) {
	svc, _ := buildRateConfigSvc(t)
	// Housing 1.5M RSD: tier 1M-2M (amount_from<=1.5M, amount_to>1.5M) => fixed=5.75;
	// margin for housing=1.50 => nominal=7.25
	baseRate, bankMargin, nominalRate, err := svc.GetNominalRateComponents("housing", "fixed", decimal.NewFromInt(1500000))
	require.NoError(t, err)
	assert.True(t, baseRate.Equal(decimal.NewFromFloat(5.75)),
		"base rate for 1M-2M tier should be 5.75; got %s", baseRate)
	assert.True(t, bankMargin.Equal(decimal.NewFromFloat(1.50)),
		"housing margin should be 1.50; got %s", bankMargin)
	assert.True(t, nominalRate.Equal(decimal.NewFromFloat(7.25)),
		"nominal rate should be 5.75+1.50=7.25; got %s", nominalRate)
}
