package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// sagaStep describes one balance-update step within a multi-service saga.
type sagaStep struct {
	name          string
	accountNumber string
	amount        decimal.Decimal // signed: negative = debit, positive = credit
	execute       func(ctx context.Context) error
}

// completedStep tracks what we need to build a compensation entry.
type completedStep struct {
	logID         uint64
	accountNumber string
	amount        decimal.Decimal
	stepName      string
}

// executeWithSaga executes each step in order, recording progress in the saga log.
// On failure at step N, it compensates steps N-1 through 0 in reverse order.
// Compensation entries that fail to execute are left in "compensating" status for
// the recovery goroutine to retry (StartCompensationRecovery on TransferService).
//
// sagaRepo may be nil; if so, saga logging is skipped (tests that don't inject a repo
// still get the correct compensation behaviour).
func executeWithSaga(
	ctx context.Context,
	sagaRepo *repository.SagaLogRepository,
	accountClient accountpb.AccountServiceClient,
	retryConfig shared.RetryConfig,
	transactionID uint64,
	txType string,
	steps []sagaStep,
) error {
	sagaID := fmt.Sprintf("%s-%d-%d", txType, transactionID, time.Now().UnixNano())
	done := make([]completedStep, 0, len(steps))

	for i, step := range steps {
		// Record the forward step before executing it.
		var logID uint64
		if sagaRepo != nil {
			entry := &model.SagaLog{
				SagaID:          sagaID,
				TransactionID:   transactionID,
				TransactionType: txType,
				StepNumber:      i + 1,
				StepName:        step.name,
				Status:          "pending",
				AccountNumber:   step.accountNumber,
				Amount:          step.amount,
				CreatedAt:       time.Now(),
			}
			if e := sagaRepo.RecordStep(entry); e != nil {
				log.Printf("saga: failed to record step %d (%s) for %s %d: %v", i+1, step.name, txType, transactionID, e)
			} else {
				logID = entry.ID
			}
		}

		if err := step.execute(ctx); err != nil {
			if sagaRepo != nil && logID != 0 {
				_ = sagaRepo.FailStep(logID, err.Error())
			}
			// Compensate all previously-completed steps in reverse order.
			TransactionSagaCompensationsTotal.Inc()
			for j := len(done) - 1; j >= 0; j-- {
				prev := done[j]
				compAmount := prev.amount.Neg()
				compLog := &model.SagaLog{
					SagaID:          sagaID,
					TransactionID:   transactionID,
					TransactionType: txType,
					StepNumber:      -(j + 1),
					StepName:        prev.stepName + "_compensation",
					Status:          "compensating",
					IsCompensation:  true,
					AccountNumber:   prev.accountNumber,
					Amount:          compAmount,
					CompensationOf:  safePtrUint64(prev.logID),
					CreatedAt:       time.Now(),
				}
				if sagaRepo != nil {
					_ = sagaRepo.RecordStep(compLog)
				}
				compErr := shared.Retry(ctx, retryConfig, func() error {
					_, e := accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
						AccountNumber:   prev.accountNumber,
						Amount:          compAmount.StringFixed(4),
						UpdateAvailable: true,
					})
					return e
				})
				if sagaRepo != nil && compLog.ID != 0 {
					if compErr != nil {
						_ = sagaRepo.FailStep(compLog.ID, compErr.Error())
					} else {
						_ = sagaRepo.CompleteStep(compLog.ID)
					}
				}
			}
			return err
		}

		// Step succeeded.
		if sagaRepo != nil && logID != 0 {
			_ = sagaRepo.CompleteStep(logID)
		}
		done = append(done, completedStep{
			logID:         logID,
			accountNumber: step.accountNumber,
			amount:        step.amount,
			stepName:      step.name,
		})
	}
	return nil
}

func safePtrUint64(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}
