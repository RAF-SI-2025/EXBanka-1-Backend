package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

func TestRetryStuckCompensation_CreditBorrower_DebitsBorrowerBack(t *testing.T) {
	db := newDisbursementTestDB(t)
	acct := &mockAccountClientForLoan{}
	saga := NewLoanDisbursementSaga(&mockBankAccountClientForLoan{}, acct, repository.NewLoanRepository(db), repository.NewSagaLogRepository(db))

	loan := &model.Loan{
		ID:            7,
		AccountNumber: "ACC-7",
		Amount:        decimal.NewFromInt(2500),
		CurrencyCode:  "RSD",
	}
	err := saga.RetryStuckCompensation(context.Background(), loan, string(sharedsaga.StepCreditBorrower))
	require.NoError(t, err)

	// Backward for credit_borrower must debit the borrower account (negative amount)
	// with the deterministic idempotency key.
	require.Len(t, acct.calls, 1)
	assert.Equal(t, "ACC-7", acct.calls[0])
	require.Len(t, acct.amounts, 1)
	assert.Equal(t, "-2500.0000", acct.amounts[0])
}

func TestRetryStuckCompensation_CreditBorrower_PropagatesClientError(t *testing.T) {
	db := newDisbursementTestDB(t)
	acct := &mockAccountClientForLoan{updateBalanceErr: errors.New("account down")}
	saga := NewLoanDisbursementSaga(&mockBankAccountClientForLoan{}, acct, repository.NewLoanRepository(db), repository.NewSagaLogRepository(db))

	loan := &model.Loan{ID: 7, AccountNumber: "ACC-7", Amount: decimal.NewFromInt(100), CurrencyCode: "RSD"}
	err := saga.RetryStuckCompensation(context.Background(), loan, string(sharedsaga.StepCreditBorrower))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account down")
}

func TestRetryStuckCompensation_MarkLoanActive_LoadError(t *testing.T) {
	db := newDisbursementTestDB(t)
	saga := NewLoanDisbursementSaga(&mockBankAccountClientForLoan{}, &mockAccountClientForLoan{}, repository.NewLoanRepository(db), repository.NewSagaLogRepository(db))

	// Loan id 999 is not in the DB → GetByID fails → wrapped error.
	loan := &model.Loan{ID: 999, AccountNumber: "ACC-X", Amount: decimal.NewFromInt(1), CurrencyCode: "RSD"}
	err := saga.RetryStuckCompensation(context.Background(), loan, string(sharedsaga.StepMarkLoanActive))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry compensation load loan")
}

func TestRetryStuckCompensation_UnknownStep(t *testing.T) {
	db := newDisbursementTestDB(t)
	saga := NewLoanDisbursementSaga(&mockBankAccountClientForLoan{}, &mockAccountClientForLoan{}, repository.NewLoanRepository(db), repository.NewSagaLogRepository(db))

	loan := &model.Loan{ID: 1, AccountNumber: "ACC-1", Amount: decimal.NewFromInt(1), CurrencyCode: "RSD"}
	err := saga.RetryStuckCompensation(context.Background(), loan, "totally_unknown")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownCompensationStep)
}

func TestRetryStuckCompensation_DebitBank_PropagatesBankError(t *testing.T) {
	db := newDisbursementTestDB(t)
	bank := &mockBankAccountClientForLoan{creditErr: errors.New("bank down")}
	saga := NewLoanDisbursementSaga(bank, &mockAccountClientForLoan{}, repository.NewLoanRepository(db), repository.NewSagaLogRepository(db))

	loan := &model.Loan{ID: 1, AccountNumber: "ACC-1", Amount: decimal.NewFromInt(100), CurrencyCode: "RSD"}
	err := saga.RetryStuckCompensation(context.Background(), loan, string(sharedsaga.StepDebitBank))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bank down")
	// The compensation must target the bank credit-back with the comp reference.
	require.Len(t, bank.creditCalls, 1)
	assert.Equal(t, "loan-disbursement-1:debit-comp", bank.creditCalls[0])
}
