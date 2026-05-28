package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// TestProcessInstallment_HappyPath covers the success path: debit borrower,
// credit bank, mark paid, no compensation.
func TestProcessInstallment_HappyPath(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	loan := &model.Loan{
		LoanNumber:    "LN-HAPPY",
		ClientID:      1,
		AccountNumber: "ACC-HAPPY",
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
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Two calls: debit borrower + credit bank.
	require.Len(t, accountClient.calls, 2)
	assert.Equal(t, "ACC-HAPPY", accountClient.calls[0].account)
	assert.Equal(t, "-1000.0000", accountClient.calls[0].amount)
	assert.Equal(t, "BANK-RSD-001", accountClient.calls[1].account)
	assert.Equal(t, "1000.0000", accountClient.calls[1].amount)

	// Installment should now be marked paid in DB.
	var got model.Installment
	require.NoError(t, db.First(&got, inst.ID).Error)
	assert.Equal(t, "paid", got.Status)
}

func TestProcessInstallment_DebitFailure_FixedLoan_NoPenalty(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	loan := &model.Loan{
		LoanNumber:    "LN-DF-FIXED",
		ClientID:      1,
		AccountNumber: "ACC-DF-FIXED",
		Status:        "active",
		CurrencyCode:  "RSD",
		InterestType:  "fixed",
		Amount:        decimal.NewFromInt(10000),
		RemainingDebt: decimal.NewFromInt(10000),
		BaseRate:      decimal.NewFromFloat(6.25),
		BankMargin:    decimal.NewFromFloat(1.75),
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

	// Account client always fails — but Retry will hammer it; we want a fast fail.
	accountClient := &mockCronAccountClient{err: errors.New("downstream failure")}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Fixed loan: rate must NOT change after a debit failure.
	var refreshed model.Loan
	require.NoError(t, db.First(&refreshed, loan.ID).Error)
	assert.True(t, refreshed.BaseRate.Equal(decimal.NewFromFloat(6.25)),
		"fixed-rate loan base rate must NOT increase after debit failure")
}

func TestProcessInstallment_DebitFailure_VariableLoan_PenaltyApplied(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	originalBase := decimal.NewFromFloat(6.25)
	loan := &model.Loan{
		LoanNumber:          "LN-DF-VAR",
		ClientID:            1,
		AccountNumber:       "ACC-DF-VAR",
		Status:              "active",
		CurrencyCode:        "RSD",
		InterestType:        "variable",
		Amount:              decimal.NewFromInt(10000),
		RemainingDebt:       decimal.NewFromInt(10000),
		BaseRate:            originalBase,
		BankMargin:          decimal.NewFromFloat(1.75),
		CurrentRate:         decimal.NewFromFloat(8.0),
		NominalInterestRate: decimal.NewFromFloat(8.0),
	}
	require.NoError(t, db.Create(loan).Error)

	// Seed an unpaid installment so CountUnpaidByLoan returns >0.
	inst := &model.Installment{
		LoanID:       loan.ID,
		Amount:       decimal.NewFromInt(1000),
		CurrencyCode: "RSD",
		Status:       "unpaid",
		ExpectedDate: time.Now().Add(-time.Hour),
	}
	require.NoError(t, db.Create(inst).Error)

	accountClient := &mockCronAccountClient{err: errors.New("downstream failure")}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Variable: BaseRate should have increased by 0.05.
	var refreshed model.Loan
	require.NoError(t, db.First(&refreshed, loan.ID).Error)
	expectedBase := originalBase.Add(decimal.NewFromFloat(0.05))
	assert.True(t, refreshed.BaseRate.Equal(expectedBase),
		"variable-rate loan base rate must increase by 0.05 after debit failure; got %s expected %s",
		refreshed.BaseRate.String(), expectedBase.String())
}

func TestProcessInstallment_LoanLookupFails(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	accountClient := &mockCronAccountClient{}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	// Loan ID 99999 doesn't exist — early return, no balance calls.
	cron.processInstallment(context.Background(), 1, 99999, "1000.0000", "RSD", "2026-03-31")
	assert.Empty(t, accountClient.calls, "no account calls when loan lookup fails")
}

// TestCollectDueInstallments_NoDueRows ensures the loop tolerates empty results.
func TestCollectDueInstallments_NoDueRows(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	accountClient := &mockCronAccountClient{}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	cron.collectDueInstallments(context.Background())
	assert.Empty(t, accountClient.calls)
}

// stagedAccountClient lets each UpdateBalance call return a different error,
// driven by a slice of errors indexed by call number.
type stagedAccountClient struct {
	mockCronAccountClient
	stagedErrors []error
	idx          int
}

func (m *stagedAccountClient) UpdateBalance(ctx context.Context, req *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	m.calls = append(m.calls, struct{ account, amount string }{req.AccountNumber, req.Amount})
	var err error
	if m.idx < len(m.stagedErrors) {
		err = m.stagedErrors[m.idx]
	}
	m.idx++
	return &accountpb.AccountResponse{}, err
}

func TestProcessInstallment_BankCreditFails_CompensatesBorrowerDebit(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	loan := &model.Loan{
		LoanNumber:    "LN-BCF",
		ClientID:      1,
		AccountNumber: "ACC-BCF",
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

	// Call 0 = debit borrower (success), Call 1 = bank credit (fail), Call 2 = compensation (success)
	accountClient := &stagedAccountClient{
		stagedErrors: []error{nil, errors.New("bank credit failure"), nil},
	}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	cron.processInstallment(context.Background(),
		inst.ID, loan.ID,
		inst.Amount.StringFixed(4), inst.CurrencyCode, "2026-03-31")

	// Expect 3 calls: debit borrower + bank credit + borrower compensation
	require.Len(t, accountClient.calls, 3)
	assert.Equal(t, "ACC-BCF", accountClient.calls[0].account, "1st: debit borrower")
	assert.Equal(t, "BANK-RSD-001", accountClient.calls[1].account, "2nd: bank credit")
	assert.Equal(t, "ACC-BCF", accountClient.calls[2].account, "3rd: compensation = credit borrower back")

	// Installment must NOT be marked paid because bank credit failed.
	var got model.Installment
	require.NoError(t, db.First(&got, inst.ID).Error)
	assert.Equal(t, "unpaid", got.Status, "installment must remain unpaid when bank credit fails")
}

// TestCronStart_CancellableViaContext starts the cron loop and immediately
// cancels the context; Start must return promptly.
func TestCronStart_CancellableViaContext(t *testing.T) {
	db := newCronTestDB(t)
	installRepo := repository.NewInstallmentRepository(db)
	loanRepo := repository.NewLoanRepository(db)

	accountClient := &mockCronAccountClient{}
	installSvc := NewInstallmentService(installRepo)
	loanSvc := NewLoanService(loanRepo)
	cron := NewCronService(installSvc, loanSvc, accountClient, nil, nil, nil, "BANK-RSD-001", db, nilRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cron.Start(ctx)
		close(done)
	}()

	// Let the first RunOnStart fire, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Cron Start did not exit within 2s of context cancel")
	}
}
