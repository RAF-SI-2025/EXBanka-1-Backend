package saga

import (
	"context"
	"errors"
	"log"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/repository"
)

// DeadLetterPublisher publishes a saga.dead-letter Kafka event when a
// compensation step exhausts its retry budget.
type DeadLetterPublisher interface {
	PublishSagaDeadLetter(ctx context.Context, msg kafkamsg.SagaDeadLetterMessage) error
}

// Classifier implements shared.Classifier for transaction-service saga
// recovery. The retry strategy mirrors what the legacy
// runRecoveryTick does: replay UpdateBalance with the recorded amount
// and account number; on success the runner marks the row terminal; on
// failure the runner increments the retry count and eventually moves
// the row to dead_letter.
type Classifier struct {
	accountClient accountpb.AccountServiceClient
	retryConfig   shared.RetryConfig
	dlPublisher   DeadLetterPublisher
	repo          *repository.SagaLogRepository
}

// NewClassifier constructs a classifier. accountClient may be nil — in
// that case all stuck rows are left for manual review (the legacy
// behavior when account-service is unwired).
func NewClassifier(client accountpb.AccountServiceClient, retryCfg shared.RetryConfig, dl DeadLetterPublisher, repo *repository.SagaLogRepository) *Classifier {
	return &Classifier{
		accountClient: client,
		retryConfig:   retryCfg,
		dlPublisher:   dl,
		repo:          repo,
	}
}

// Compile-time guard.
var _ shared.Classifier = (*Classifier)(nil)

// Classify routes every stuck step to ActionRetry as long as we have an
// account client + amount/account in the payload. Forward steps in
// pending status are also retryable because the legacy executor treated
// them the same way: replay UpdateBalance, idempotency_key on the
// account-service ledger absorbs duplicates.
func (c *Classifier) Classify(ctx context.Context, step shared.StuckStep) shared.RecoveryAction {
	if c.accountClient == nil {
		return shared.ActionLeave
	}
	acct, _ := step.Payload["account_number"].(string)
	if acct == "" {
		return shared.ActionLeave
	}
	if _, ok := step.Payload["amount"]; !ok {
		return shared.ActionLeave
	}
	return shared.ActionRetry
}

// Retry replays UpdateBalance for the row's recorded account+amount.
func (c *Classifier) Retry(ctx context.Context, step shared.StuckStep) error {
	if c.accountClient == nil {
		return errors.New("classifier: nil account client")
	}
	acct, _ := step.Payload["account_number"].(string)
	if acct == "" {
		return errors.New("classifier: empty account number on row")
	}
	amountAny, ok := step.Payload["amount"]
	if !ok {
		return errors.New("classifier: no amount on row")
	}
	amount, ok := amountAny.(decimal.Decimal)
	if !ok {
		return errors.New("classifier: amount payload is not decimal.Decimal")
	}

	return shared.Retry(ctx, c.retryConfig, func() error {
		_, e := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   acct,
			Amount:          amount.StringFixed(4),
			UpdateAvailable: true,
		})
		return e
	})
}

// PublishDeadLetter sends the saga.dead-letter Kafka event for a row
// that's exhausted its retry budget. Called by the recovery runner via
// a side-channel hook because shared.Classifier doesn't model
// dead-lettering itself.
func (c *Classifier) PublishDeadLetter(ctx context.Context, step shared.StuckStep, reason string) {
	if c.dlPublisher == nil {
		return
	}
	acct, _ := step.Payload["account_number"].(string)
	amount, _ := step.Payload["amount"].(decimal.Decimal)
	txID, _ := step.Payload["transaction_id"].(uint64)
	txType, _ := step.Payload["transaction_type"].(string)
	msg := kafkamsg.SagaDeadLetterMessage{
		SagaLogID:       step.Handle.ID,
		SagaID:          step.SagaID,
		TransactionID:   txID,
		TransactionType: txType,
		StepName:        step.StepName,
		AccountNumber:   acct,
		Amount:          amount.StringFixed(4),
		RetryCount:      step.RetryCount,
		LastError:       reason,
	}
	if err := c.dlPublisher.PublishSagaDeadLetter(ctx, msg); err != nil {
		log.Printf("classifier: dead-letter publish failed for saga step %d: %v", step.Handle.ID, err)
	}
}
