package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// TestStartCompensationRecovery_NilSagaRepo verifies that calling
// StartCompensationRecovery when sagaRepo is nil does not panic.
func TestStartCompensationRecovery_NilSagaRepo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := &TransferService{
		sagaRepo:      nil,
		accountClient: nil,
		dlPublisher:   nil,
	}

	// Must not panic; cancel context to stop the background goroutine.
	assert.NotPanics(t, func() {
		svc.StartCompensationRecovery(ctx)
	})
}

// TestStartCompensationRecovery_RunsImmediately verifies that the recovery tick
// executes synchronously before StartCompensationRecovery returns.
//
// A compensating saga step is seeded in the DB. The test calls
// StartCompensationRecovery and immediately — without sleeping or waiting for any
// ticker — checks that the step transitioned to "completed". If recovery were only
// scheduled on the 5-minute ticker, the status would still be "compensating" here.
func TestStartCompensationRecovery_RunsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	comp := model.SagaLog{
		SagaID:          "startup-recovery-test",
		TransactionID:   42,
		TransactionType: "transfer",
		StepNumber:      1,
		StepName:        "debit_sender",
		Status:          "compensating",
		IsCompensation:  true,
		AccountNumber:   "ACC-STARTUP-001",
		Amount:          decimal.NewFromInt(300),
		RetryCount:      0,
	}
	require.NoError(t, db.Create(&comp).Error)

	// Account client that always succeeds.
	accountClient := &mockAccountClientForTransfer{failOnCall: 0}

	svc := &TransferService{
		sagaRepo:      sagaRepo,
		accountClient: accountClient,
		dlPublisher:   nil,
		retryConfig:   shared.RetryConfig{MaxAttempts: 1},
	}

	// StartCompensationRecovery must run the tick synchronously before returning.
	svc.StartCompensationRecovery(ctx)

	// No sleep: the tick happened before the call returned.
	var updated model.SagaLog
	require.NoError(t, db.First(&updated, comp.ID).Error)
	assert.Equal(t, "completed", updated.Status,
		"compensation step must be completed immediately at startup, not deferred to the ticker")
}

// TestStartCompensationRecovery_RunsImmediately_UpdateBalanceCalled verifies the
// immediate execution by counting UpdateBalance invocations. If recovery were only
// on the 5-minute ticker, the call count would be zero immediately after the
// function returns (no ticker fires in a test environment in under a second).
func TestStartCompensationRecovery_RunsImmediately_UpdateBalanceCalled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newTransferTestDB(t)
	sagaRepo := repository.NewSagaLogRepository(db)

	comp := model.SagaLog{
		SagaID:          "startup-count-test",
		TransactionID:   43,
		TransactionType: "payment",
		StepNumber:      1,
		StepName:        "credit_recipient",
		Status:          "compensating",
		IsCompensation:  true,
		AccountNumber:   "ACC-COUNTED-001",
		Amount:          decimal.NewFromInt(150),
		RetryCount:      0,
	}
	require.NoError(t, db.Create(&comp).Error)

	// failOnCall=0 means never fail; we can observe callCount afterward.
	accountClient := &mockAccountClientForTransfer{failOnCall: 0}

	svc := &TransferService{
		sagaRepo:      sagaRepo,
		accountClient: accountClient,
		dlPublisher:   nil,
		retryConfig:   shared.RetryConfig{MaxAttempts: 1},
	}

	start := time.Now()
	svc.StartCompensationRecovery(ctx)
	elapsed := time.Since(start)

	assert.Equal(t, 1, accountClient.callCount,
		"UpdateBalance must be called exactly once during startup recovery")
	assert.Less(t, elapsed, 5*time.Second,
		"StartCompensationRecovery must not block for the 5-minute ticker interval")
}
