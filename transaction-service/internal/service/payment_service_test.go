package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// TestExecutePayment_CommissionCreditInSaga verifies that when a payment has
// commission, the bank RSD account receives a credit as the 3rd saga step.
func TestExecutePayment_CommissionCreditInSaga(t *testing.T) {
	ctx := context.Background()
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"ACC-FROM-001": 101,
			"ACC-TO-001":   202,
		},
	}
	feeSvc := newTestFeeService(1000, 0.001) // 0.1% on amounts >= 1000
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-001", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	p := &model.Payment{
		IdempotencyKey:    "test-commission-saga-001",
		FromAccountNumber: "ACC-FROM-001",
		ToAccountNumber:   "ACC-TO-001",
		InitialAmount:     decimal.NewFromInt(10000),
		CurrencyCode:      "RSD",
		Status:            "pending_verification",
	}
	require.NoError(t, svc.CreatePayment(ctx, p))
	require.NoError(t, svc.ExecutePayment(ctx, p.ID))

	// Expect exactly 3 UpdateBalance calls: debit sender, credit recipient, credit bank
	require.Len(t, accountClient.calls, 3, "must have exactly 3 UpdateBalance calls")
	assert.Equal(t, "BANK-RSD-001", accountClient.calls[2].accountNumber,
		"3rd call must be credit_bank_commission to BANK-RSD-001")
	commAmt, _ := decimal.NewFromString(accountClient.calls[2].amount)
	assert.True(t, commAmt.IsPositive(), "commission amount must be positive (credit)")
}

// TestExecutePayment_CommissionFailureCompensates verifies that if the commission
// step (3rd call) fails, the debit_sender and credit_recipient steps are both reversed.
func TestExecutePayment_CommissionFailureCompensates(t *testing.T) {
	ctx := context.Background()
	// failOnCall: 3 = the commission credit call fails
	accountClient := &mockAccountClientForTransfer{
		failOnCall: 3,
		ownerOverrides: map[string]uint64{
			"ACC-FROM-002": 101,
			"ACC-TO-002":   202,
		},
	}
	feeSvc := newTestFeeService(1000, 0.001)
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-001", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	p := &model.Payment{
		IdempotencyKey:    "test-commission-comp-001",
		FromAccountNumber: "ACC-FROM-002",
		ToAccountNumber:   "ACC-TO-002",
		InitialAmount:     decimal.NewFromInt(10000),
		CurrencyCode:      "RSD",
		Status:            "pending_verification",
	}
	require.NoError(t, svc.CreatePayment(ctx, p))
	err := svc.ExecutePayment(ctx, p.ID)
	assert.Error(t, err, "payment must fail when commission step fails")

	// Calls: (1) debit_sender, (2) credit_recipient, (3) commission [FAIL],
	//        (4) compensate credit_recipient → reverse, (5) compensate debit_sender → reverse
	require.Len(t, accountClient.calls, 5,
		"expected debit + credit + failed commission + 2 compensation reversals")
	// Compensation calls have reversed signs
	compRecipient, _ := decimal.NewFromString(accountClient.calls[3].amount)
	compSender, _ := decimal.NewFromString(accountClient.calls[4].amount)
	assert.True(t, compRecipient.IsNegative(),
		"compensation of credit_recipient must be negative (debit back)")
	assert.True(t, compSender.IsPositive(),
		"compensation of debit_sender must be positive (credit back)")
}
