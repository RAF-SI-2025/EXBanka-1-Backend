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

func newBankMarginRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.BankMargin{}))
	return db
}

func TestBankMarginRepo_Count_Empty(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	count, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestBankMarginRepo_CreateAndGetByID(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	m := &model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}
	require.NoError(t, repo.Create(m))
	assert.NotZero(t, m.ID)

	got, err := repo.GetByID(m.ID)
	require.NoError(t, err)
	assert.Equal(t, "cash", got.LoanType)
	assert.True(t, got.Margin.Equal(decimal.NewFromFloat(1.75)))
}

func TestBankMarginRepo_GetByID_NotFound(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	_, err := repo.GetByID(99999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestBankMarginRepo_FindByLoanType_Active(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	require.NoError(t, repo.Create(&model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}))
	require.NoError(t, repo.Create(&model.BankMargin{LoanType: "auto", Margin: decimal.NewFromFloat(1.25), Active: true}))

	got, err := repo.FindByLoanType("cash")
	require.NoError(t, err)
	assert.Equal(t, "cash", got.LoanType)
}

func TestBankMarginRepo_FindByLoanType_InactiveSkipped(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	m := &model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}
	require.NoError(t, repo.Create(m))
	// Flip to inactive post-insert so the gorm default:true doesn't override it.
	require.NoError(t, db.Model(m).Update("active", false).Error)

	_, err := repo.FindByLoanType("cash")
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestBankMarginRepo_FindByLoanType_Missing(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	_, err := repo.FindByLoanType("nonexistent")
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestBankMarginRepo_ListAll(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	require.NoError(t, repo.Create(&model.BankMargin{LoanType: "housing", Margin: decimal.NewFromFloat(1.50), Active: true}))
	require.NoError(t, repo.Create(&model.BankMargin{LoanType: "auto", Margin: decimal.NewFromFloat(1.25), Active: true}))
	require.NoError(t, repo.Create(&model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}))
	old := &model.BankMargin{LoanType: "old", Margin: decimal.NewFromFloat(2.0), Active: true}
	require.NoError(t, repo.Create(old))
	require.NoError(t, db.Model(old).Update("active", false).Error)

	got, err := repo.ListAll()
	require.NoError(t, err)
	assert.Len(t, got, 3, "inactive margins are excluded")
	// Should be sorted by loan_type ASC
	assert.Equal(t, "auto", got[0].LoanType)
	assert.Equal(t, "cash", got[1].LoanType)
	assert.Equal(t, "housing", got[2].LoanType)
}

func TestBankMarginRepo_Count_Mixed(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	require.NoError(t, repo.Create(&model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}))
	old := &model.BankMargin{LoanType: "old", Margin: decimal.NewFromFloat(2.0), Active: true}
	require.NoError(t, repo.Create(old))
	require.NoError(t, db.Model(old).Update("active", false).Error)

	count, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count, "Count returns total including inactive")
}

func TestBankMarginRepo_Update(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	m := &model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}
	require.NoError(t, repo.Create(m))

	m.Margin = decimal.NewFromFloat(2.5)
	require.NoError(t, repo.Update(m))

	got, err := repo.GetByID(m.ID)
	require.NoError(t, err)
	assert.True(t, got.Margin.Equal(decimal.NewFromFloat(2.5)))
}

func TestBankMarginRepo_Delete(t *testing.T) {
	db := newBankMarginRepoDB(t)
	repo := NewBankMarginRepository(db)

	m := &model.BankMargin{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true}
	require.NoError(t, repo.Create(m))

	require.NoError(t, repo.Delete(m.ID))

	_, err := repo.GetByID(m.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}
