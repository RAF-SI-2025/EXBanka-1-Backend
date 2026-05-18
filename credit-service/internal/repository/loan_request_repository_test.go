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

func newLoanRequestRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.LoanRequest{}))
	return db
}

func newSampleLoanRequest(account string, status string) *model.LoanRequest {
	return &model.LoanRequest{
		ClientID:        1,
		LoanType:        "cash",
		InterestType:    "fixed",
		Amount:          decimal.NewFromInt(150000),
		CurrencyCode:    "RSD",
		RepaymentPeriod: 12,
		AccountNumber:   account,
		Status:          status,
	}
}

func TestLoanRequestRepo_CreateAndGetByID(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	req := newSampleLoanRequest("ACC-CR-1", "pending")
	require.NoError(t, repo.Create(req))
	assert.NotZero(t, req.ID)

	got, err := repo.GetByID(req.ID)
	require.NoError(t, err)
	assert.Equal(t, "ACC-CR-1", got.AccountNumber)
	assert.Equal(t, "pending", got.Status)
}

func TestLoanRequestRepo_GetByID_NotFound(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	_, err := repo.GetByID(99999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestLoanRequestRepo_GetByIDForUpdate(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	req := newSampleLoanRequest("ACC-FU-1", "pending")
	require.NoError(t, repo.Create(req))

	// SELECT FOR UPDATE inside a TX
	err := db.Transaction(func(tx *gorm.DB) error {
		got, err := repo.GetByIDForUpdate(tx, req.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, req.ID, got.ID)
		return nil
	})
	require.NoError(t, err)
}

func TestLoanRequestRepo_GetByIDForUpdate_NotFound(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := repo.GetByIDForUpdate(tx, 99999)
		return err
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestLoanRequestRepo_Update_VersionedSuccess(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	req := newSampleLoanRequest("ACC-UP-1", "pending")
	require.NoError(t, repo.Create(req))

	fresh, err := repo.GetByID(req.ID)
	require.NoError(t, err)
	fresh.Status = "approved"
	require.NoError(t, repo.Update(fresh))

	got, err := repo.GetByID(req.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", got.Status)
}

func TestLoanRequestRepo_List_FiltersAndPagination(t *testing.T) {
	db := newLoanRequestRepoDB(t)
	repo := NewLoanRequestRepository(db)

	// Seed mix of requests
	for i := 0; i < 5; i++ {
		r := newSampleLoanRequest(fmt.Sprintf("ACC-F-%d", i), "pending")
		r.ClientID = 1
		require.NoError(t, repo.Create(r))
	}
	r2 := newSampleLoanRequest("ACC-F-X1", "approved")
	r2.ClientID = 1
	r2.LoanType = "housing"
	r2.RepaymentPeriod = 240
	require.NoError(t, repo.Create(r2))

	r3 := newSampleLoanRequest("ACC-F-X2", "rejected")
	r3.ClientID = 2
	require.NoError(t, repo.Create(r3))

	// No filter, paginated
	got, total, err := repo.List("", "", "", 0, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(7), total)
	assert.Len(t, got, 3)

	// Filter by client
	got, total, err = repo.List("", "", "", 2, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)

	// Filter by loan type
	got, total, err = repo.List("housing", "", "", 0, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)

	// Filter by status
	_, total, err = repo.List("", "", "approved", 0, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// Filter by account
	_, total, err = repo.List("", "ACC-F-X1", "", 0, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// Negative offset clamped
	got, total, err = repo.List("", "", "", 0, 0, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(7), total)
	assert.Len(t, got, 3)
}
