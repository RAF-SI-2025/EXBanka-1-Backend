package service

import (
	"context"
	"fmt"

	accountpb "github.com/exbanka/contract/accountpb"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	creditsaga "github.com/exbanka/credit-service/internal/saga"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// ErrUnknownCompensationStep is returned by RetryStuckCompensation when the
// given stepName is not recognized as a compensatable disbursement step.
var ErrUnknownCompensationStep = fmt.Errorf("unknown compensation step")

// LoanDisbursementSaga coordinates the three-step loan disbursement:
//  1. debit_bank       — debit the bank-owned sentinel account for the loan currency.
//  2. credit_borrower  — credit the borrower's selected account.
//  3. mark_loan_active — flip loan.Status to "active".
//
// Each step has a backward compensation. The shared saga runner executes
// compensations in reverse on failure; if a compensation itself fails, the
// row is left in "compensating" for the recovery worker to retry.
type LoanDisbursementSaga struct {
	bankClient    accountpb.BankAccountServiceClient
	accountClient accountpb.AccountServiceClient
	loanRepo      *repository.LoanRepository
	sagaRepo      *repository.SagaLogRepository
}

// NewLoanDisbursementSaga constructs a saga ready to disburse any loan.
func NewLoanDisbursementSaga(
	bank accountpb.BankAccountServiceClient,
	acct accountpb.AccountServiceClient,
	loanRepo *repository.LoanRepository,
	sagaRepo *repository.SagaLogRepository,
) *LoanDisbursementSaga {
	return &LoanDisbursementSaga{
		bankClient:    bank,
		accountClient: acct,
		loanRepo:      loanRepo,
		sagaRepo:      sagaRepo,
	}
}

// Disburse runs the three-step saga. Idempotent: if the loan is already
// "active" the call returns nil immediately.
func (s *LoanDisbursementSaga) Disburse(ctx context.Context, loan *model.Loan) error {
	if loan.Status == "active" {
		return nil
	}

	sagaID := fmt.Sprintf("loan-disbursement-%d", loan.ID)
	amountStr := loan.Amount.StringFixed(4)
	currency := loan.CurrencyCode
	borrowerAccount := loan.AccountNumber

	recorder := creditsaga.NewRecorder(s.sagaRepo, loan.ID)

	state := sharedsaga.NewState()
	// Pre-populate per-step audit metadata consumed by the recorder.
	state.Set("step:"+string(sharedsaga.StepDebitBank)+":account_number", "bank:"+currency)
	state.Set("step:"+string(sharedsaga.StepDebitBank)+":amount", loan.Amount.Neg()) // debit = negative
	state.Set("step:"+string(sharedsaga.StepCreditBorrower)+":account_number", borrowerAccount)
	state.Set("step:"+string(sharedsaga.StepCreditBorrower)+":amount", loan.Amount) // credit = positive

	sg := sharedsaga.NewSagaWithID(sagaID, recorder)

	sg.Add(sharedsaga.Step{
		Name: sharedsaga.StepDebitBank,
		Forward: func(ctx context.Context, _ *sharedsaga.State) error {
			_, err := s.bankClient.DebitBankAccount(ctx, &accountpb.BankAccountOpRequest{
				Currency:  currency,
				Amount:    amountStr,
				Reference: sagaID + ":debit",
				Reason:    fmt.Sprintf("loan %d disbursement", loan.ID),
			})
			return err
		},
		Backward: func(ctx context.Context, _ *sharedsaga.State) error {
			_, err := s.bankClient.CreditBankAccount(ctx, &accountpb.BankAccountOpRequest{
				Currency:  currency,
				Amount:    amountStr,
				Reference: sagaID + ":debit-comp",
				Reason:    fmt.Sprintf("loan %d disbursement compensation", loan.ID),
			})
			return err
		},
	})

	sg.Add(sharedsaga.Step{
		Name: sharedsaga.StepCreditBorrower,
		Forward: func(ctx context.Context, _ *sharedsaga.State) error {
			_, err := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   borrowerAccount,
				Amount:          amountStr,
				UpdateAvailable: true,
				IdempotencyKey:  sagaID + ":credit",
			})
			return err
		},
		Backward: func(ctx context.Context, _ *sharedsaga.State) error {
			neg := loan.Amount.Neg().StringFixed(4)
			_, err := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   borrowerAccount,
				Amount:          neg,
				UpdateAvailable: true,
				IdempotencyKey:  sagaID + ":credit-comp",
			})
			return err
		},
	})

	sg.Add(sharedsaga.Step{
		Name: sharedsaga.StepMarkLoanActive,
		Forward: func(ctx context.Context, _ *sharedsaga.State) error {
			loan.Status = "active"
			return s.loanRepo.Update(loan)
		},
		Backward: func(ctx context.Context, _ *sharedsaga.State) error {
			loan.Status = "disbursement_failed"
			return s.loanRepo.Update(loan)
		},
	})

	return sg.Execute(ctx, state)
}

// RetryStuckCompensation re-executes ONLY the Backward closure for the named
// step. This is what the recovery worker must call instead of Disburse: calling
// Disburse would re-run the full forward saga, which on a partially-compensated
// loan can double-credit the borrower or re-debit the bank in the wrong direction.
//
// The idempotency keys used here are the same as those in the original Disburse
// backward closures, so safe re-execution is guaranteed.
func (s *LoanDisbursementSaga) RetryStuckCompensation(ctx context.Context, loan *model.Loan, stepName string) error {
	sagaID := fmt.Sprintf("loan-disbursement-%d", loan.ID)
	amountStr := loan.Amount.StringFixed(4)
	currency := loan.CurrencyCode
	borrowerAccount := loan.AccountNumber

	switch stepName {
	case string(sharedsaga.StepDebitBank):
		// Backward for debit_bank: credit the bank account back (reverse the
		// initial debit). Reference key matches the original compensation key.
		_, err := s.bankClient.CreditBankAccount(ctx, &accountpb.BankAccountOpRequest{
			Currency:  currency,
			Amount:    amountStr,
			Reference: sagaID + ":debit-comp",
			Reason:    fmt.Sprintf("loan %d disbursement compensation (recovery)", loan.ID),
		})
		return err

	case string(sharedsaga.StepCreditBorrower):
		// Backward for credit_borrower: debit the borrower's account (reverse
		// the credit already applied). Idempotency key matches original.
		neg := loan.Amount.Neg().StringFixed(4)
		_, err := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   borrowerAccount,
			Amount:          neg,
			UpdateAvailable: true,
			IdempotencyKey:  sagaID + ":credit-comp",
		})
		return err

	case string(sharedsaga.StepMarkLoanActive):
		// Backward for mark_loan_active: flip status back to disbursement_failed.
		// Load fresh to avoid stale-version issues on retry.
		fresh, err := s.loanRepo.GetByID(loan.ID)
		if err != nil {
			return fmt.Errorf("retry compensation load loan: %w", err)
		}
		fresh.Status = "disbursement_failed"
		return s.loanRepo.Update(fresh)

	default:
		return fmt.Errorf("%w: %q", ErrUnknownCompensationStep, stepName)
	}
}
