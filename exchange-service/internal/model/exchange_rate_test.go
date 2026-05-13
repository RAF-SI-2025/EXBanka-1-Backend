package model_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/exchange-service/internal/model"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	return db
}

func TestExchangeRate_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := setupTestDB(t)

	// Create a rate with version 1.
	rate := model.ExchangeRate{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		BuyRate:      decimal.NewFromFloat(116.5),
		SellRate:     decimal.NewFromFloat(118.5),
		Version:      1,
	}
	require.NoError(t, db.Create(&rate).Error)
	assert.Equal(t, int64(1), rate.Version)

	// Update via Save — the BeforeUpdate hook should increment the version.
	rate.SellRate = decimal.NewFromFloat(119.0)
	result := db.Save(&rate)
	require.NoError(t, result.Error)
	assert.Equal(t, int64(1), result.RowsAffected)
	assert.Equal(t, int64(2), rate.Version)

	// Re-read from DB to confirm persisted version.
	var reloaded model.ExchangeRate
	require.NoError(t, db.First(&reloaded, rate.ID).Error)
	assert.Equal(t, int64(2), reloaded.Version)
	assert.True(t, reloaded.SellRate.Equal(decimal.NewFromFloat(119.0)))
}

func TestExchangeRate_BeforeUpdate_OptimisticLockConflict(t *testing.T) {
	db := setupTestDB(t)

	// Create initial rate.
	rate := model.ExchangeRate{
		FromCurrency: "USD",
		ToCurrency:   "RSD",
		BuyRate:      decimal.NewFromFloat(106.0),
		SellRate:     decimal.NewFromFloat(108.0),
		Version:      1,
	}
	require.NoError(t, db.Create(&rate).Error)

	// Load a "stale" copy (simulating a concurrent reader).
	var stale model.ExchangeRate
	require.NoError(t, db.First(&stale, rate.ID).Error)
	assert.Equal(t, int64(1), stale.Version)

	// Update the "real" copy — version goes from 1 to 2.
	rate.BuyRate = decimal.NewFromFloat(107.0)
	result := db.Save(&rate)
	require.NoError(t, result.Error)
	assert.Equal(t, int64(1), result.RowsAffected)

	// The stale copy still has Version=1. The hook will add WHERE version=1,
	// but the DB row already has version=2. Using db.Model().Updates() to
	// trigger the hook via an explicit WHERE on the struct (not Save, which
	// uses primary key and may bypass the version WHERE in SQLite).
	staleResult := db.Model(&stale).Updates(model.ExchangeRate{
		BuyRate: decimal.NewFromFloat(105.0),
	})
	require.NoError(t, staleResult.Error) // no SQL error, just 0 rows
	assert.Equal(t, int64(0), staleResult.RowsAffected, "stale update should affect 0 rows due to version mismatch")

	// Verify the DB still has the "real" update, not the stale one.
	var final model.ExchangeRate
	require.NoError(t, db.First(&final, rate.ID).Error)
	assert.Equal(t, int64(2), final.Version)
	assert.True(t, final.BuyRate.Equal(decimal.NewFromFloat(107.0)), "DB should retain the non-stale update")
}

// fakeSeedRepo is a minimal in-memory implementation of the GetByPair/Upsert
// surface used by SeedDefaultRates. It tracks calls so tests can assert seed
// behaviour without a real database.
type fakeSeedRepo struct {
	existing map[string]*model.ExchangeRate
	upserts  map[string]*model.ExchangeRate
	getErr   error
}

func newFakeSeedRepo() *fakeSeedRepo {
	return &fakeSeedRepo{
		existing: make(map[string]*model.ExchangeRate),
		upserts:  make(map[string]*model.ExchangeRate),
	}
}

func (f *fakeSeedRepo) key(from, to string) string { return from + "/" + to }

func (f *fakeSeedRepo) GetByPair(from, to string) (*model.ExchangeRate, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if r, ok := f.existing[f.key(from, to)]; ok {
		return r, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeSeedRepo) Upsert(from, to string, buy, sell decimal.Decimal) error {
	f.upserts[f.key(from, to)] = &model.ExchangeRate{
		FromCurrency: from,
		ToCurrency:   to,
		BuyRate:      buy,
		SellRate:     sell,
	}
	return nil
}

// TestSeedDefaultRates_SeedsAllPairsWhenEmpty verifies that on an empty repo,
// SeedDefaultRates writes every default pair (forward + inverse for each currency).
func TestSeedDefaultRates_SeedsAllPairsWhenEmpty(t *testing.T) {
	repo := newFakeSeedRepo()
	model.SeedDefaultRates(repo)
	// 7 currencies × 2 directions = 14 pairs.
	assert.Equal(t, 14, len(repo.upserts))
	// Spot-check a couple of representative pairs.
	eurRsd, ok := repo.upserts["EUR/RSD"]
	require.True(t, ok)
	assert.True(t, eurRsd.SellRate.Equal(decimal.NewFromFloat(118.00)))
	rsdEur, ok := repo.upserts["RSD/EUR"]
	require.True(t, ok)
	assert.True(t, rsdEur.SellRate.Equal(decimal.NewFromFloat(0.00862)))
}

// TestSeedDefaultRates_SkipsExisting verifies that existing pairs are not
// re-seeded — only missing ones are inserted.
func TestSeedDefaultRates_SkipsExisting(t *testing.T) {
	repo := newFakeSeedRepo()
	repo.existing["EUR/RSD"] = &model.ExchangeRate{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		BuyRate:      decimal.NewFromFloat(1),
		SellRate:     decimal.NewFromFloat(2),
	}
	model.SeedDefaultRates(repo)
	// EUR/RSD was already present and must NOT have been re-upserted.
	_, gotEurRsd := repo.upserts["EUR/RSD"]
	assert.False(t, gotEurRsd, "EUR/RSD should not be re-seeded since it already exists")
	// All others should still have been seeded — 13 pairs.
	assert.Equal(t, 13, len(repo.upserts))
}

// TestSeedDefaultRates_GetErrorSkipsPair verifies that a non-NotFound DB error
// from GetByPair causes the pair to be skipped (logged), not re-inserted.
func TestSeedDefaultRates_GetErrorSkipsPair(t *testing.T) {
	repo := newFakeSeedRepo()
	repo.getErr = errors.New("transient db error")
	model.SeedDefaultRates(repo)
	// All pairs short-circuit on the GetByPair error and never reach Upsert.
	assert.Equal(t, 0, len(repo.upserts))
}
