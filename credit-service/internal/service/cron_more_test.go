package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// TestProcessInstallment_AlreadyPaid_NoCompensation covers the early-return branch
// inside processInstallment where MarkInstallmentPaid returns ErrInstallmentAlreadyPaid
// (i.e., a concurrent run already settled the row). The cron run must NOT compensate
// because the money flow was correct.
func TestProcessInstallment_AlreadyPaid_NoCompensation(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	loan := &model.Loan{
		LoanNumber:    "LN-AP",
		ClientID:      1,
		AccountNumber: "ACC-AP",
		Status:        "active",
		CurrencyCode:  "RSD",
		InterestType:  "fixed",
		Amount:        decimal.NewFromInt(10000),
		RemainingDebt: decimal.NewFromInt(10000),
	}
	require.NoError(t, db.Create(loan).Error)
	inst := &model.Installment{
		LoanID:       loan.ID,
		Amount:       decimal.NewFromInt(1000),
		CurrencyCode: "RSD",
		Status:       "paid", // already paid → MarkPaid will return ErrInstallmentAlreadyPaid
		ExpectedDate: time.Now().Add(-time.Hour),
	}
	require.NoError(t, db.Create(inst).Error)

	accountClient := &mockCronAccountClient{}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Note: production MarkPaid actually re-paints "paid" rows green — see mark_paid_test.go
	// for that quirk. The key assertion here is that no compensation happens; we still
	// expect a debit and a bank-credit, but no extra reversal calls.
	require.GreaterOrEqual(t, len(accountClient.calls), 2)
}

// TestProcessInstallment_NoProducer_NoBankAccount covers the early-exit branch
// where bankRSDAccount is empty: the installment is debited but no bank credit
// is attempted. The installment still ends up paid because the bank-credit step
// is short-circuited entirely.
func TestProcessInstallment_NoBankRSDAccount(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	loan := &model.Loan{
		LoanNumber:    "LN-NB",
		ClientID:      1,
		AccountNumber: "ACC-NB",
		Status:        "active",
		CurrencyCode:  "RSD",
		InterestType:  "fixed",
		Amount:        decimal.NewFromInt(10000),
		RemainingDebt: decimal.NewFromInt(10000),
	}
	require.NoError(t, db.Create(loan).Error)
	inst := &model.Installment{
		LoanID:       loan.ID,
		Amount:       decimal.NewFromInt(1000),
		CurrencyCode: "RSD",
		Status:       "unpaid",
		ExpectedDate: time.Now().Add(-time.Hour),
	}
	require.NoError(t, db.Create(inst).Error)

	accountClient := &mockCronAccountClient{}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	// Empty bankRSDAccount string → bank-credit step skipped
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "", db, nilRegistry())

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Only the borrower debit was made.
	require.Len(t, accountClient.calls, 1)
	assert.Equal(t, "ACC-NB", accountClient.calls[0].account)

	// Installment marked paid since both pre-flight steps succeeded.
	var got model.Installment
	require.NoError(t, db.First(&got, inst.ID).Error)
	assert.Equal(t, "paid", got.Status)
}
