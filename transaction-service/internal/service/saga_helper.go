package service

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	shared "github.com/exbanka/contract/shared"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/transaction-service/internal/repository"
	tsaga "github.com/exbanka/transaction-service/internal/saga"
)

// sagaStep describes one balance-update step within a multi-service saga.
// Each step is implicitly compensatable: on failure of any subsequent step,
// the saga calls accountClient.UpdateBalance with the negated amount on the
// same account number to roll back this step's effect.
type sagaStep struct {
	name          sharedsaga.StepKind
	accountNumber string
	amount        decimal.Decimal // signed: negative = debit, positive = credit
	execute       func(ctx context.Context) error
}

// executeWithSaga executes each step in order using the shared saga runner,
// recording progress through the per-service Recorder adapter and rolling
// back via accountClient.UpdateBalance on the negated amount when a later
// step fails. Compensation entries that fail to execute are left in
// "compensating" status for the recovery loop to retry.
//
// sagaRepo may be nil; if so, saga logging is skipped (tests that don't
// inject a repo still get the correct compensation behaviour).
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

	var recorder sharedsaga.Recorder = sharedsaga.NoopRecorder{}
	if sagaRepo != nil {
		recorder = tsaga.NewRecorder(sagaRepo)
	}

	state := sharedsaga.NewState()
	state.Set("transaction_id", transactionID)
	state.Set("transaction_type", txType)

	sg := sharedsaga.NewSagaWithID(sagaID, recorder)

	for _, step := range steps {
		step := step
		// Pre-populate per-step audit metadata so the recorder can persist
		// the right account/amount when sharedsaga.Saga writes the row.
		state.Set("step:"+string(step.name)+":account_number", step.accountNumber)
		state.Set("step:"+string(step.name)+":amount", step.amount)

		sg.Add(sharedsaga.Step{
			Name: step.name,
			Forward: func(ctx context.Context, _ *sharedsaga.State) error {
				return step.execute(ctx)
			},
			Backward: func(ctx context.Context, _ *sharedsaga.State) error {
				if accountClient == nil {
					return nil
				}
				compAmount := step.amount.Neg()
				return shared.Retry(ctx, retryConfig, func() error {
					_, e := accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
						AccountNumber:   step.accountNumber,
						Amount:          compAmount.StringFixed(4),
						UpdateAvailable: true,
					})
					return e
				})
			},
		})
	}

	if err := sg.Execute(ctx, state); err != nil {
		// Match legacy metric semantics: increment once when the saga as a
		// whole fails (not once per compensation step). sg.Execute returns
		// only after all rollback work is done, so a single increment here
		// captures the saga-level failure.
		TransactionSagaCompensationsTotal.Inc()
		return err
	}
	return nil
}
