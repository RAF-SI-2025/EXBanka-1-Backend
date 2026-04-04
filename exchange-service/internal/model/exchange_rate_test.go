package model_test

import (
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
