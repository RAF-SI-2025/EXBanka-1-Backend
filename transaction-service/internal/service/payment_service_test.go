package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// recordingNotifier is a test stub for the notifier interface. It records every
// GeneralNotificationMessage emitted by the service under test so assertions can
// inspect Type, UserID, Data, RefType, and RefID. Shared across payment and
// transfer service tests (same package).
type recordingNotifier struct {
	notifs []kafkamsg.GeneralNotificationMessage
}

func (r *recordingNotifier) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.notifs = append(r.notifs, m)
	return nil
}

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

// TestPaymentService_ExecutePayment_EmitsSenderAndReceiverNotifications verifies
// that a happy-path payment emits a PAYMENT_SENT notification to the sender and
// a PAYMENT_RECEIVED notification to the receiver, using the Data form.
func TestPaymentService_ExecutePayment_EmitsSenderAndReceiverNotifications(t *testing.T) {
	ctx := context.Background()
	const senderOwnerID uint64 = 101
	const receiverOwnerID uint64 = 202
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"ACC-FROM-NOTIF": senderOwnerID,
			"ACC-TO-NOTIF":   receiverOwnerID,
		},
	}
	feeSvc := newTestFeeService(0, 0) // no commission step
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-001", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	rec := &recordingNotifier{}
	svc.notifier = rec

	p := &model.Payment{
		IdempotencyKey:    "test-notif-happy-001",
		FromAccountNumber: "ACC-FROM-NOTIF",
		ToAccountNumber:   "ACC-TO-NOTIF",
		InitialAmount:     decimal.NewFromInt(500),
		CurrencyCode:      "RSD",
		Status:            "pending_verification",
	}
	require.NoError(t, svc.CreatePayment(ctx, p))
	require.NoError(t, svc.ExecutePayment(ctx, p.ID))

	require.Len(t, rec.notifs, 2, "expected exactly 2 notifications (sender + receiver)")

	var sent, received *kafkamsg.GeneralNotificationMessage
	for i := range rec.notifs {
		switch rec.notifs[i].Type {
		case "PAYMENT_SENT":
			sent = &rec.notifs[i]
		case "PAYMENT_RECEIVED":
			received = &rec.notifs[i]
		}
	}

	require.NotNil(t, sent, "PAYMENT_SENT must be emitted")
	assert.Equal(t, senderOwnerID, sent.UserID, "PAYMENT_SENT UserID must be sender owner")
	assert.Equal(t, "payment", sent.RefType)
	assert.Equal(t, p.ID, sent.RefID)
	assert.NotEmpty(t, sent.Data["amount"], "PAYMENT_SENT Data.amount must be set")
	assert.Equal(t, p.ToAccountNumber, sent.Data["to_account"], "PAYMENT_SENT Data.to_account must equal payment ToAccountNumber")

	require.NotNil(t, received, "PAYMENT_RECEIVED must be emitted")
	assert.Equal(t, receiverOwnerID, received.UserID, "PAYMENT_RECEIVED UserID must be receiver owner")
	assert.Equal(t, "payment", received.RefType)
	assert.Equal(t, p.ID, received.RefID)
	assert.NotEmpty(t, received.Data["amount"], "PAYMENT_RECEIVED Data.amount must be set")
	assert.Equal(t, p.FromAccountNumber, received.Data["from_account"], "PAYMENT_RECEIVED Data.from_account must equal payment FromAccountNumber")
}

// TestPaymentService_ExecutePayment_SkipsBankOwnedSide verifies that when one
// side of the payment is bank-owned (owner_id == 1_000_000_000), that side's
// notification is skipped. Here, the sender is the bank — only PAYMENT_RECEIVED
// should be emitted.
func TestPaymentService_ExecutePayment_SkipsBankOwnedSide(t *testing.T) {
	ctx := context.Background()
	const bankSentinel uint64 = 1_000_000_000
	const receiverOwnerID uint64 = 202
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"BANK-FROM-NOTIF": bankSentinel,
			"ACC-TO-NOTIF-2":  receiverOwnerID,
		},
	}
	// CreatePayment validates that fromOwnerID != toOwnerID; bank vs client → ok.
	feeSvc := newTestFeeService(0, 0)
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-001", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	rec := &recordingNotifier{}
	svc.notifier = rec

	p := &model.Payment{
		IdempotencyKey:    "test-notif-bank-skip-001",
		FromAccountNumber: "BANK-FROM-NOTIF",
		ToAccountNumber:   "ACC-TO-NOTIF-2",
		InitialAmount:     decimal.NewFromInt(500),
		CurrencyCode:      "RSD",
		Status:            "pending_verification",
	}
	require.NoError(t, svc.CreatePayment(ctx, p))
	require.NoError(t, svc.ExecutePayment(ctx, p.ID))

	// No PAYMENT_SENT (sender is the bank); PAYMENT_RECEIVED must be present.
	for _, n := range rec.notifs {
		assert.NotEqual(t, "PAYMENT_SENT", n.Type, "PAYMENT_SENT must be skipped for bank-owned sender")
	}
	var received *kafkamsg.GeneralNotificationMessage
	for i := range rec.notifs {
		if rec.notifs[i].Type == "PAYMENT_RECEIVED" {
			received = &rec.notifs[i]
			break
		}
	}
	require.NotNil(t, received, "PAYMENT_RECEIVED must still be emitted for client receiver")
	assert.Equal(t, receiverOwnerID, received.UserID)
}

// TestPaymentService_ExecutePayment_FailedSagaEmitsFailedNotification verifies
// that when the saga fails (e.g., debit_sender fails on first call), exactly
// one PAYMENT_FAILED notification is emitted to the sender, with the
// failure_reason and amount in Data.
func TestPaymentService_ExecutePayment_FailedSagaEmitsFailedNotification(t *testing.T) {
	ctx := context.Background()
	const senderOwnerID uint64 = 101
	const receiverOwnerID uint64 = 202
	// failOnCall: 1 = debit_sender fails immediately. No compensation needed since no
	// prior steps ran. CreatePayment does TWO GetAccountByNumber lookups but those
	// don't increment UpdateBalance callCount, so the 1st UpdateBalance call is the
	// debit-sender step in ExecutePayment.
	accountClient := &mockAccountClientForTransfer{
		failOnCall: 1,
		ownerOverrides: map[string]uint64{
			"ACC-FROM-FAIL": senderOwnerID,
			"ACC-TO-FAIL":   receiverOwnerID,
		},
	}
	feeSvc := newTestFeeService(0, 0)
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-001", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	rec := &recordingNotifier{}
	svc.notifier = rec

	p := &model.Payment{
		IdempotencyKey:    "test-notif-fail-001",
		FromAccountNumber: "ACC-FROM-FAIL",
		ToAccountNumber:   "ACC-TO-FAIL",
		InitialAmount:     decimal.NewFromInt(500),
		CurrencyCode:      "RSD",
		Status:            "pending_verification",
	}
	require.NoError(t, svc.CreatePayment(ctx, p))
	err := svc.ExecutePayment(ctx, p.ID)
	require.Error(t, err, "ExecutePayment must error when saga step fails")

	// Exactly one PAYMENT_FAILED, no PAYMENT_SENT/PAYMENT_RECEIVED.
	var failed *kafkamsg.GeneralNotificationMessage
	for i := range rec.notifs {
		switch rec.notifs[i].Type {
		case "PAYMENT_SENT", "PAYMENT_RECEIVED":
			t.Fatalf("unexpected success-path notification %q on failed saga", rec.notifs[i].Type)
		case "PAYMENT_FAILED":
			require.Nil(t, failed, "must emit at most one PAYMENT_FAILED")
			failed = &rec.notifs[i]
		}
	}
	require.NotNil(t, failed, "PAYMENT_FAILED must be emitted on saga failure")
	assert.Equal(t, senderOwnerID, failed.UserID, "PAYMENT_FAILED UserID must be sender owner")
	assert.Equal(t, "payment", failed.RefType)
	assert.Equal(t, p.ID, failed.RefID)
	assert.NotEmpty(t, failed.Data["amount"], "PAYMENT_FAILED Data.amount must be set")
	assert.NotEmpty(t, failed.Data["failure_reason"], "PAYMENT_FAILED Data.failure_reason must be set")
}
