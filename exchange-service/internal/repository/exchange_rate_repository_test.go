package repository_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/repository"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	return db
}

func TestUpsertAndGetByPair(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewExchangeRateRepository(db)

	buy := decimal.NewFromFloat(116.5)
	sell := decimal.NewFromFloat(118.5)

	// First upsert — create
	err := repo.Upsert("EUR", "RSD", buy, sell)
	require.NoError(t, err)

	rate, err := repo.GetByPair("EUR", "RSD")
	require.NoError(t, err)
	assert.Equal(t, "EUR", rate.FromCurrency)
	assert.Equal(t, "RSD", rate.ToCurrency)
	assert.True(t, rate.SellRate.Equal(sell))
	assert.Equal(t, int64(1), rate.Version)

	// Second upsert — update, version increments
	sell2 := decimal.NewFromFloat(119.0)
	err = repo.Upsert("EUR", "RSD", buy, sell2)
	require.NoError(t, err)

	rate2, err := repo.GetByPair("EUR", "RSD")
	require.NoError(t, err)
	assert.True(t, rate2.SellRate.Equal(sell2))
	assert.Equal(t, int64(2), rate2.Version)
}

func TestList(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewExchangeRateRepository(db)

	_ = repo.Upsert("EUR", "RSD", decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.0))
	_ = repo.Upsert("RSD", "EUR", decimal.NewFromFloat(0.00844), decimal.NewFromFloat(0.00858))

	rates, err := repo.List()
	require.NoError(t, err)
	assert.Len(t, rates, 2)
}
