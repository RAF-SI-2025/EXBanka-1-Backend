package repository

import (
	"errors"
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
)

func newInstallmentRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Installment{}))
	return db
}

func newSampleInstallment(loanID uint64, expected time.Time, status string) *model.Installment {
	return &model.Installment{
		LoanID:       loanID,
		Amount:       decimal.NewFromInt(1000),
		InterestRate: decimal.NewFromFloat(8.0),
		CurrencyCode: "RSD",
		ExpectedDate: expected,
		Status:       status,
	}
}

func TestInstallmentRepo_CreateBatch_AndListByLoan(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	now := time.Now()
	batch := []model.Installment{
		*newSampleInstallment(1, now.AddDate(0, 1, 0), "unpaid"),
		*newSampleInstallment(1, now.AddDate(0, 2, 0), "unpaid"),
		*newSampleInstallment(1, now.AddDate(0, 3, 0), "unpaid"),
		*newSampleInstallment(2, now.AddDate(0, 1, 0), "unpaid"),
	}
	require.NoError(t, repo.CreateBatch(batch))

	got, err := repo.ListByLoan(1)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	// Asserts ordered by expected_date asc
	assert.True(t, got[0].ExpectedDate.Before(got[1].ExpectedDate))
	assert.True(t, got[1].ExpectedDate.Before(got[2].ExpectedDate))

	got2, err := repo.ListByLoan(2)
	require.NoError(t, err)
	assert.Len(t, got2, 1)

	gotEmpty, err := repo.ListByLoan(99999)
	require.NoError(t, err)
	assert.Empty(t, gotEmpty)
}

func TestInstallmentRepo_MarkPaid_Success(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	inst := newSampleInstallment(1, time.Now().AddDate(0, 1, 0), "unpaid")
	require.NoError(t, db.Create(inst).Error)

	require.NoError(t, repo.MarkPaid(inst.ID))

	var refreshed model.Installment
	require.NoError(t, db.First(&refreshed, inst.ID).Error)
	assert.Equal(t, "paid", refreshed.Status)
	require.NotNil(t, refreshed.ActualDate)
}

func TestInstallmentRepo_MarkPaid_NotFound(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	err := repo.MarkPaid(99999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInstallmentNotFound))
}

func TestInstallmentRepo_MarkOverdue(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	now := time.Now()
	overdue := newSampleInstallment(1, now.AddDate(0, 0, -5), "unpaid")
	dueFuture := newSampleInstallment(1, now.AddDate(0, 1, 0), "unpaid")
	pastPaid := newSampleInstallment(1, now.AddDate(0, 0, -10), "paid")
	require.NoError(t, db.Create(overdue).Error)
	require.NoError(t, db.Create(dueFuture).Error)
	require.NoError(t, db.Create(pastPaid).Error)

	require.NoError(t, repo.MarkOverdue())

	var refOverdue model.Installment
	require.NoError(t, db.First(&refOverdue, overdue.ID).Error)
	assert.Equal(t, "overdue", refOverdue.Status)

	var refFuture model.Installment
	require.NoError(t, db.First(&refFuture, dueFuture.ID).Error)
	assert.Equal(t, "unpaid", refFuture.Status, "future unpaid should remain unpaid")

	var refPaid model.Installment
	require.NoError(t, db.First(&refPaid, pastPaid.ID).Error)
	assert.Equal(t, "paid", refPaid.Status, "already-paid should not flip to overdue")
}

func TestInstallmentRepo_GetDueInstallments(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	now := time.Now()
	overdue := newSampleInstallment(1, now.AddDate(0, 0, -5), "unpaid")
	dueNow := newSampleInstallment(1, now.Add(-time.Hour), "unpaid")
	future := newSampleInstallment(1, now.AddDate(0, 1, 0), "unpaid")
	paid := newSampleInstallment(1, now.AddDate(0, 0, -10), "paid")
	require.NoError(t, db.Create(overdue).Error)
	require.NoError(t, db.Create(dueNow).Error)
	require.NoError(t, db.Create(future).Error)
	require.NoError(t, db.Create(paid).Error)

	got, err := repo.GetDueInstallments()
	require.NoError(t, err)
	assert.Len(t, got, 2, "overdue + due-now, but not future or paid")
}

func TestInstallmentRepo_CountUnpaidByLoan(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	now := time.Now()
	require.NoError(t, db.Create(newSampleInstallment(1, now.AddDate(0, 1, 0), "unpaid")).Error)
	require.NoError(t, db.Create(newSampleInstallment(1, now.AddDate(0, 2, 0), "unpaid")).Error)
	require.NoError(t, db.Create(newSampleInstallment(1, now.AddDate(0, 3, 0), "paid")).Error)
	require.NoError(t, db.Create(newSampleInstallment(2, now.AddDate(0, 1, 0), "unpaid")).Error)

	count, err := repo.CountUnpaidByLoan(1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = repo.CountUnpaidByLoan(2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = repo.CountUnpaidByLoan(99)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestInstallmentRepo_UpdateUnpaidByLoan(t *testing.T) {
	db := newInstallmentRepoDB(t)
	repo := NewInstallmentRepository(db)

	now := time.Now()
	a := newSampleInstallment(1, now.AddDate(0, 1, 0), "unpaid")
	b := newSampleInstallment(1, now.AddDate(0, 2, 0), "unpaid")
	c := newSampleInstallment(1, now.AddDate(0, 3, 0), "paid") // should not be touched
	require.NoError(t, db.Create(a).Error)
	require.NoError(t, db.Create(b).Error)
	require.NoError(t, db.Create(c).Error)

	newAmount := decimal.NewFromInt(2222)
	newRate := decimal.NewFromFloat(9.99)
	require.NoError(t, repo.UpdateUnpaidByLoan(1, newAmount, newRate))

	var gotA model.Installment
	require.NoError(t, db.First(&gotA, a.ID).Error)
	assert.True(t, gotA.Amount.Equal(newAmount))
	assert.True(t, gotA.InterestRate.Equal(newRate))

	var gotB model.Installment
	require.NoError(t, db.First(&gotB, b.ID).Error)
	assert.True(t, gotB.Amount.Equal(newAmount))

	// Paid installment should not be touched
	var gotC model.Installment
	require.NoError(t, db.First(&gotC, c.ID).Error)
	assert.True(t, gotC.Amount.Equal(decimal.NewFromInt(1000)),
		"paid installment must not be modified by UpdateUnpaidByLoan")
}
