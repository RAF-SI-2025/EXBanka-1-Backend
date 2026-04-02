package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

func newTransferTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.SagaLog{}))
	return db
}

// TestSagaRecovery_DeadLetterAfterMaxRetries verifies that after maxSagaCompensationRetries
// failures, the saga step is moved to dead_letter and a Kafka event is published.
func TestSagaRecovery_DeadLetterAfterMaxRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	// Seed a compensating step that has already been retried 9 times
	comp := model.SagaLog{
		SagaID:          "saga-dlq-test",
		TransactionID:   999,
		TransactionType: "transfer",
		StepNumber:      1,
		StepName:        "debit_sender",
		Status:          "compensating",
		IsCompensation:  true,
		AccountNumber:   "ACC-DEAD-001",
		Amount:          decimal.NewFromInt(500),
		RetryCount:      9, // one more failure → dead_letter
	}
	require.NoError(t, db.Create(&comp).Error)

	// Account client that always fails
	accountClient := &mockAccountClientForTransfer{failOnCall: 1}

	// Capture published dead-letter messages
	var dlMessages []kafkamsg.SagaDeadLetterMessage
	mockProducer := &mockDeadLetterProducer{capture: &dlMessages}

	svc := newTransferServiceForTest(sagaRepo, accountClient, mockProducer)
	// Trigger one recovery tick synchronously
	svc.runRecoveryTick(ctx)

	// Verify the step was moved to dead_letter in the DB
	var updated model.SagaLog
	require.NoError(t, db.First(&updated, comp.ID).Error)
	assert.Equal(t, "dead_letter", updated.Status, "status must be dead_letter after max retries")

	// Verify a dead-letter Kafka event was published
	require.Len(t, dlMessages, 1, "must publish exactly one dead-letter message")
	assert.Equal(t, uint64(999), dlMessages[0].TransactionID)
	assert.Equal(t, 10, dlMessages[0].RetryCount)
}

// TestSagaRecovery_BelowMaxRetries_NoDeadLetter verifies that a step below the retry
// threshold is NOT moved to dead_letter and no Kafka event is published.
func TestSagaRecovery_BelowMaxRetries_NoDeadLetter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	comp := model.SagaLog{
		SagaID:          "saga-below-threshold",
		TransactionID:   888,
		TransactionType: "transfer",
		StepNumber:      1,
		StepName:        "debit_sender",
		Status:          "compensating",
		IsCompensation:  true,
		AccountNumber:   "ACC-RETRY-001",
		Amount:          decimal.NewFromInt(100),
		RetryCount:      5, // 5 + 1 = 6, below threshold of 10
	}
	require.NoError(t, db.Create(&comp).Error)

	accountClient := &mockAccountClientForTransfer{failOnCall: 1}

	var dlMessages []kafkamsg.SagaDeadLetterMessage
	mockProducer := &mockDeadLetterProducer{capture: &dlMessages}

	svc := newTransferServiceForTest(sagaRepo, accountClient, mockProducer)
	svc.runRecoveryTick(ctx)

	// Status should still be "compensating"
	var updated model.SagaLog
	require.NoError(t, db.First(&updated, comp.ID).Error)
	assert.Equal(t, "compensating", updated.Status, "status must remain compensating below threshold")
	assert.Equal(t, 6, updated.RetryCount, "retry_count must be incremented")

	// No dead-letter message
	assert.Empty(t, dlMessages, "no dead-letter message below threshold")
}

// TestSagaRecovery_SuccessCompletesStep verifies that a successful UpdateBalance
// marks the step as completed.
func TestSagaRecovery_SuccessCompletesStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	comp := model.SagaLog{
		SagaID:          "saga-success",
		TransactionID:   777,
		TransactionType: "transfer",
		StepNumber:      1,
		StepName:        "credit_recipient",
		Status:          "compensating",
		IsCompensation:  true,
		AccountNumber:   "ACC-OK-001",
		Amount:          decimal.NewFromInt(200),
		RetryCount:      3,
	}
	require.NoError(t, db.Create(&comp).Error)

	// Never fails
	accountClient := &mockAccountClientForTransfer{failOnCall: 0}

	var dlMessages []kafkamsg.SagaDeadLetterMessage
	mockProducer := &mockDeadLetterProducer{capture: &dlMessages}

	svc := newTransferServiceForTest(sagaRepo, accountClient, mockProducer)
	svc.runRecoveryTick(ctx)

	var updated model.SagaLog
	require.NoError(t, db.First(&updated, comp.ID).Error)
	assert.Equal(t, "completed", updated.Status, "status must be completed after successful retry")
	assert.Empty(t, dlMessages, "no dead-letter message on success")
}

// mockDeadLetterProducer satisfies the sagaPublisher interface.
type mockDeadLetterProducer struct {
	capture *[]kafkamsg.SagaDeadLetterMessage
}

func (m *mockDeadLetterProducer) PublishSagaDeadLetter(_ context.Context, msg kafkamsg.SagaDeadLetterMessage) error {
	*m.capture = append(*m.capture, msg)
	return nil
}

// newTransferServiceForTest creates a minimal TransferService for testing the recovery logic.
func newTransferServiceForTest(sagaRepo *repository.SagaLogRepository, accountClient accountpb.AccountServiceClient, dlPublisher sagaPublisher) *TransferService {
	return &TransferService{
		sagaRepo:      sagaRepo,
		accountClient: accountClient,
		dlPublisher:   dlPublisher,
		retryConfig:   shared.RetryConfig{MaxAttempts: 1},
	}
}
