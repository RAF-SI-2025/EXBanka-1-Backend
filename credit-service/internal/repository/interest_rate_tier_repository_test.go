package repository

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
)

func newTierRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.InterestRateTier{}))
	return db
}

func TestTierRepo_Count_Empty(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	count, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestTierRepo_CreateAndGetByID(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	tier := &model.InterestRateTier{
		AmountFrom:   decimal.NewFromInt(0),
		AmountTo:     decimal.NewFromInt(500_000),
		FixedRate:    decimal.NewFromFloat(6.25),
		VariableBase: decimal.NewFromFloat(6.25),
		Active:       true,
	}
	require.NoError(t, repo.Create(tier))
	assert.NotZero(t, tier.ID)

	got, err := repo.GetByID(tier.ID)
	require.NoError(t, err)
	assert.True(t, got.AmountTo.Equal(decimal.NewFromInt(500_000)))
}

func TestTierRepo_GetByID_NotFound(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	_, err := repo.GetByID(99999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestTierRepo_FindByAmount_BoundedTier(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	t1 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000),
		FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true}
	t2 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(500_000), AmountTo: decimal.NewFromInt(1_000_000),
		FixedRate: decimal.NewFromFloat(6.0), VariableBase: decimal.NewFromFloat(6.0), Active: true}
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.Create(t2))

	got, err := repo.FindByAmount(decimal.NewFromInt(100_000))
	require.NoError(t, err)
	assert.True(t, got.FixedRate.Equal(decimal.NewFromFloat(6.25)))

	got, err = repo.FindByAmount(decimal.NewFromInt(750_000))
	require.NoError(t, err)
	assert.True(t, got.FixedRate.Equal(decimal.NewFromFloat(6.0)))
}

func TestTierRepo_FindByAmount_UnlimitedTier(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	// Highest tier with AmountTo=0 (treated as unlimited)
	highTier := &model.InterestRateTier{
		AmountFrom: decimal.NewFromInt(20_000_000), AmountTo: decimal.Zero,
		FixedRate: decimal.NewFromFloat(4.75), VariableBase: decimal.NewFromFloat(4.75), Active: true,
	}
	require.NoError(t, repo.Create(highTier))

	got, err := repo.FindByAmount(decimal.NewFromInt(50_000_000))
	require.NoError(t, err)
	assert.True(t, got.FixedRate.Equal(decimal.NewFromFloat(4.75)))
}

func TestTierRepo_FindByAmount_NoMatch(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	tier := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000),
		FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true}
	require.NoError(t, repo.Create(tier))

	// Asking for amount > tier max — no match
	_, err := repo.FindByAmount(decimal.NewFromInt(1_000_000))
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestTierRepo_ListAll_OrderedByAmountFromAsc(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	t3 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(2_000_000), AmountTo: decimal.NewFromInt(5_000_000),
		FixedRate: decimal.NewFromFloat(5.5), VariableBase: decimal.NewFromFloat(5.5), Active: true}
	t1 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000),
		FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true}
	t2 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(500_000), AmountTo: decimal.NewFromInt(1_000_000),
		FixedRate: decimal.NewFromFloat(6.0), VariableBase: decimal.NewFromFloat(6.0), Active: true}
	tInactive := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(10_000_000), AmountTo: decimal.NewFromInt(20_000_000),
		FixedRate: decimal.NewFromFloat(5.0), VariableBase: decimal.NewFromFloat(5.0), Active: true}
	require.NoError(t, repo.Create(t3))
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.Create(t2))
	require.NoError(t, repo.Create(tInactive))
	// Flip the last one inactive after insert (avoids GORM's default-on-zero-value behavior).
	require.NoError(t, db.Model(tInactive).Update("active", false).Error)

	got, err := repo.ListAll()
	require.NoError(t, err)
	assert.Len(t, got, 3, "inactive tiers excluded from ListAll")
	assert.True(t, got[0].AmountFrom.Equal(decimal.NewFromInt(0)))
	assert.True(t, got[1].AmountFrom.Equal(decimal.NewFromInt(500_000)))
	assert.True(t, got[2].AmountFrom.Equal(decimal.NewFromInt(2_000_000)))
}

func TestTierRepo_Count_Mixed(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	t1 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000),
		FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true}
	t2 := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(500_000), AmountTo: decimal.NewFromInt(1_000_000),
		FixedRate: decimal.NewFromFloat(6.0), VariableBase: decimal.NewFromFloat(6.0), Active: true}
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.Create(t2))
	require.NoError(t, db.Model(t2).Update("active", false).Error)

	count, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count, "Count returns total including inactive")
}

func TestTierRepo_Update(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	tier := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000),
		FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true}
	require.NoError(t, repo.Create(tier))

	tier.FixedRate = decimal.NewFromFloat(7.0)
	require.NoError(t, repo.Update(tier))

	got, err := repo.GetByID(tier.ID)
	require.NoError(t, err)
	assert.True(t, got.FixedRate.Equal(decimal.NewFromFloat(7.0)))
}

func TestTierRepo_Delete(t *testing.T) {
	db := newTierRepoDB(t)
	repo := NewInterestRateTierRepository(db)

	tier := &model.InterestRateTier{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000),
		FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true}
	require.NoError(t, repo.Create(tier))

	require.NoError(t, repo.Delete(tier.ID))

	_, err := repo.GetByID(tier.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}
