package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

func newInstallmentExtraDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Installment{}, &model.Loan{}))
	return db
}

func TestInstallmentService_GetInstallmentsByLoan(t *testing.T) {
	db := newInstallmentExtraDB(t)
	repo := repository.NewInstallmentRepository(db)
	svc := NewInstallmentService(repo)

	now := time.Now()
	installments := []model.Installment{
		{LoanID: 100, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD", ExpectedDate: now.AddDate(0, 1, 0), Status: "unpaid"},
		{LoanID: 100, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD", ExpectedDate: now.AddDate(0, 2, 0), Status: "unpaid"},
		{LoanID: 200, Amount: decimal.NewFromInt(500), CurrencyCode: "RSD", ExpectedDate: now.AddDate(0, 1, 0), Status: "unpaid"},
	}
	for i := range installments {
		require.NoError(t, db.Create(&installments[i]).Error)
	}

	got, err := svc.GetInstallmentsByLoan(100)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	for _, inst := range got {
		assert.Equal(t, uint64(100), inst.LoanID)
	}

	got2, err := svc.GetInstallmentsByLoan(200)
	require.NoError(t, err)
	assert.Len(t, got2, 1)

	gotEmpty, err := svc.GetInstallmentsByLoan(999)
	require.NoError(t, err)
	assert.Empty(t, gotEmpty)
}

func TestInstallmentService_GetDueInstallments(t *testing.T) {
	db := newInstallmentExtraDB(t)
	repo := repository.NewInstallmentRepository(db)
	svc := NewInstallmentService(repo)

	now := time.Now()
	overdue := &model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now.AddDate(0, 0, -5), Status: "unpaid"}
	dueToday := &model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now, Status: "unpaid"}
	future := &model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now.AddDate(0, 1, 0), Status: "unpaid"}
	paidPast := &model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now.AddDate(0, 0, -10), Status: "paid"}
	require.NoError(t, db.Create(overdue).Error)
	require.NoError(t, db.Create(dueToday).Error)
	require.NoError(t, db.Create(future).Error)
	require.NoError(t, db.Create(paidPast).Error)

	due, err := svc.GetDueInstallments()
	require.NoError(t, err)
	assert.Len(t, due, 2, "overdue + due-today, but not paid or future")
}

func TestInstallmentService_MarkOverdueInstallments(t *testing.T) {
	db := newInstallmentExtraDB(t)
	repo := repository.NewInstallmentRepository(db)
	svc := NewInstallmentService(repo)

	now := time.Now()
	require.NoError(t, db.Create(&model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now.AddDate(0, 0, -5), Status: "unpaid"}).Error)
	require.NoError(t, db.Create(&model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: now.AddDate(0, 1, 0), Status: "unpaid"}).Error)

	require.NoError(t, svc.MarkOverdueInstallments())

	var overdue []model.Installment
	require.NoError(t, db.Where("status = ?", "overdue").Find(&overdue).Error)
	assert.Len(t, overdue, 1, "only past-due unpaid should be flipped to overdue")
}

func TestInstallmentService_MarkInstallmentPaid(t *testing.T) {
	db := newInstallmentExtraDB(t)
	repo := repository.NewInstallmentRepository(db)
	svc := NewInstallmentService(repo)

	inst := &model.Installment{LoanID: 1, Amount: decimal.NewFromInt(1000), CurrencyCode: "RSD",
		ExpectedDate: time.Now().AddDate(0, 1, 0), Status: "unpaid"}
	require.NoError(t, db.Create(inst).Error)

	require.NoError(t, svc.MarkInstallmentPaid(inst.ID))

	var updated model.Installment
	require.NoError(t, db.First(&updated, inst.ID).Error)
	assert.Equal(t, "paid", updated.Status)
	require.NotNil(t, updated.ActualDate)
}
