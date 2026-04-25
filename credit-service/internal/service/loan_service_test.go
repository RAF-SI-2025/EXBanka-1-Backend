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

func newLoanSvcTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Loan{}))
	return db
}

func seedLoan(t *testing.T, db *gorm.DB, clientID uint64, loanType, account, status string, amount decimal.Decimal) *model.Loan {
	t.Helper()
	loan := &model.Loan{
		LoanNumber:    fmt.Sprintf("LN%d-%s", clientID, account),
		LoanType:      loanType,
		AccountNumber: account,
		Amount:        amount,
		CurrencyCode:  "RSD",
		InterestType:  "fixed",
		Status:        status,
		ClientID:      clientID,
		RemainingDebt: amount,
	}
	require.NoError(t, db.Create(loan).Error)
	return loan
}

func TestLoanService_GetLoan_Found(t *testing.T) {
	db := newLoanSvcTestDB(t)
	repo := repository.NewLoanRepository(db)
	svc := NewLoanService(repo)
	loan := seedLoan(t, db, 1, "cash", "ACC-1", "active", decimal.NewFromInt(10000))

	got, err := svc.GetLoan(loan.ID)
	require.NoError(t, err)
	assert.Equal(t, loan.ID, got.ID)
	assert.Equal(t, "cash", got.LoanType)
	assert.Equal(t, "ACC-1", got.AccountNumber)
}

func TestLoanService_GetLoan_NotFound(t *testing.T) {
	db := newLoanSvcTestDB(t)
	repo := repository.NewLoanRepository(db)
	svc := NewLoanService(repo)

	_, err := svc.GetLoan(9999)
	require.Error(t, err)
}

func TestLoanService_ListLoansByClient_FiltersByClient(t *testing.T) {
	db := newLoanSvcTestDB(t)
	repo := repository.NewLoanRepository(db)
	svc := NewLoanService(repo)
	seedLoan(t, db, 1, "cash", "ACC-1", "active", decimal.NewFromInt(10000))
	seedLoan(t, db, 1, "housing", "ACC-2", "active", decimal.NewFromInt(2000000))
	seedLoan(t, db, 2, "cash", "ACC-3", "active", decimal.NewFromInt(5000))

	loans, total, err := svc.ListLoansByClient(1, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "client 1 has 2 loans")
	assert.Len(t, loans, 2)

	loans2, total2, err := svc.ListLoansByClient(2, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total2)
	assert.Len(t, loans2, 1)
}

func TestLoanService_ListAllLoans_NoFilters(t *testing.T) {
	db := newLoanSvcTestDB(t)
	repo := repository.NewLoanRepository(db)
	svc := NewLoanService(repo)
	seedLoan(t, db, 1, "cash", "ACC-1", "active", decimal.NewFromInt(10000))
	seedLoan(t, db, 2, "housing", "ACC-2", "approved", decimal.NewFromInt(2000000))

	loans, total, err := svc.ListAllLoans("", "", "", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, loans, 2)
}

func TestLoanService_ListAllLoans_FiltersByLoanType(t *testing.T) {
	db := newLoanSvcTestDB(t)
	repo := repository.NewLoanRepository(db)
	svc := NewLoanService(repo)
	seedLoan(t, db, 1, "cash", "ACC-1", "active", decimal.NewFromInt(10000))
	seedLoan(t, db, 2, "housing", "ACC-2", "active", decimal.NewFromInt(2000000))
	seedLoan(t, db, 3, "cash", "ACC-3", "active", decimal.NewFromInt(20000))

	loans, total, err := svc.ListAllLoans("cash", "", "", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	for _, l := range loans {
		assert.Equal(t, "cash", l.LoanType)
	}
}

func TestLoanService_ListAllLoans_FiltersByAccountAndStatus(t *testing.T) {
	db := newLoanSvcTestDB(t)
	repo := repository.NewLoanRepository(db)
	svc := NewLoanService(repo)
	seedLoan(t, db, 1, "cash", "ACC-XYZ", "active", decimal.NewFromInt(10000))
	seedLoan(t, db, 2, "housing", "ACC-XYZ", "approved", decimal.NewFromInt(2000000))
	seedLoan(t, db, 3, "cash", "OTHER", "active", decimal.NewFromInt(20000))

	_, total, err := svc.ListAllLoans("", "ACC-XYZ", "", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	loansActive, totalActive, err := svc.ListAllLoans("", "", "active", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalActive)
	for _, l := range loansActive {
		assert.Equal(t, "active", l.Status)
	}
}
