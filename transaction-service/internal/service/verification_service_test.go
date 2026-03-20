package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// setupTestDB creates an in-memory SQLite DB with the required tables
// and returns repositories and the verification service.
func setupTestDB(t *testing.T) (*repository.VerificationCodeRepository, *repository.PaymentRepository, *repository.TransferRepository, *VerificationService, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&model.VerificationCode{}, &model.Payment{}, &model.Transfer{})
	require.NoError(t, err)

	vcRepo := repository.NewVerificationCodeRepository(db)
	paymentRepo := repository.NewPaymentRepository(db)
	transferRepo := repository.NewTransferRepository(db)

	svc := NewVerificationService(vcRepo, paymentRepo, transferRepo)
	return vcRepo, paymentRepo, transferRepo, svc, db
}

func TestValidateVerificationCode_CancelPaymentOnMaxAttempts(t *testing.T) {
	_, paymentRepo, _, svc, db := setupTestDB(t)

	// Create a payment in "pending" status
	payment := &model.Payment{Status: "pending"}
	require.NoError(t, db.Create(payment).Error)

	// Create a verification code for this payment
	vc := &model.VerificationCode{
		ClientID:        1,
		TransactionID:   uint64(payment.ID),
		TransactionType: "payment",
		Code:            "123456",
		ExpiresAt:       time.Now().Add(5 * time.Minute),
		Attempts:        0,
		Used:            false,
	}
	require.NoError(t, db.Create(vc).Error)

	// Use 3 wrong attempts
	for i := 0; i < 2; i++ {
		valid, remaining, err := svc.ValidateVerificationCode(1, uint64(payment.ID), "payment", "000000")
		assert.False(t, valid)
		assert.NoError(t, err)
		assert.Equal(t, 2-i, remaining)
	}

	// Third wrong attempt should cancel the transaction
	valid, remaining, err := svc.ValidateVerificationCode(1, uint64(payment.ID), "payment", "000000")
	assert.False(t, valid)
	assert.Equal(t, 0, remaining)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction cancelled")

	// Verify the payment status was changed to "rejected"
	updatedPayment, err := paymentRepo.GetByID(uint64(payment.ID))
	require.NoError(t, err)
	assert.Equal(t, "rejected", updatedPayment.Status)
}

func TestValidateVerificationCode_CancelTransferOnMaxAttempts(t *testing.T) {
	_, _, transferRepo, svc, db := setupTestDB(t)

	// Create a transfer in "pending" status
	transfer := &model.Transfer{Status: "pending"}
	require.NoError(t, db.Create(transfer).Error)

	// Create a verification code for this transfer
	vc := &model.VerificationCode{
		ClientID:        1,
		TransactionID:   uint64(transfer.ID),
		TransactionType: "transfer",
		Code:            "123456",
		ExpiresAt:       time.Now().Add(5 * time.Minute),
		Attempts:        0,
		Used:            false,
	}
	require.NoError(t, db.Create(vc).Error)

	// Exhaust all 3 attempts with wrong code
	for i := 0; i < 2; i++ {
		valid, _, err := svc.ValidateVerificationCode(1, uint64(transfer.ID), "transfer", "000000")
		assert.False(t, valid)
		assert.NoError(t, err)
	}

	// Third wrong attempt should cancel the transfer
	valid, remaining, err := svc.ValidateVerificationCode(1, uint64(transfer.ID), "transfer", "000000")
	assert.False(t, valid)
	assert.Equal(t, 0, remaining)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction cancelled")

	// Verify the transfer status was changed to "rejected"
	updatedTransfer, err := transferRepo.GetByID(uint64(transfer.ID))
	require.NoError(t, err)
	assert.Equal(t, "rejected", updatedTransfer.Status)
}

func TestValidateVerificationCode_CancelOnAlreadyMaxAttempts(t *testing.T) {
	_, paymentRepo, _, svc, db := setupTestDB(t)

	// Create a payment in "pending" status
	payment := &model.Payment{Status: "pending"}
	require.NoError(t, db.Create(payment).Error)

	// Create a verification code that already has maxAttempts reached
	vc := &model.VerificationCode{
		ClientID:        1,
		TransactionID:   uint64(payment.ID),
		TransactionType: "payment",
		Code:            "123456",
		ExpiresAt:       time.Now().Add(5 * time.Minute),
		Attempts:        3, // already at max
		Used:            false,
	}
	require.NoError(t, db.Create(vc).Error)

	// Any subsequent attempt should cancel and return error
	valid, remaining, err := svc.ValidateVerificationCode(1, uint64(payment.ID), "payment", "123456")
	assert.False(t, valid)
	assert.Equal(t, 0, remaining)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction cancelled")

	// Verify the payment was rejected
	updatedPayment, err := paymentRepo.GetByID(uint64(payment.ID))
	require.NoError(t, err)
	assert.Equal(t, "rejected", updatedPayment.Status)
}

func TestValidateVerificationCode_SuccessBeforeMaxAttempts(t *testing.T) {
	_, paymentRepo, _, svc, db := setupTestDB(t)

	// Create a payment in "pending" status
	payment := &model.Payment{Status: "pending"}
	require.NoError(t, db.Create(payment).Error)

	// Create a verification code
	vc := &model.VerificationCode{
		ClientID:        1,
		TransactionID:   uint64(payment.ID),
		TransactionType: "payment",
		Code:            "123456",
		ExpiresAt:       time.Now().Add(5 * time.Minute),
		Attempts:        0,
		Used:            false,
	}
	require.NoError(t, db.Create(vc).Error)

	// One wrong attempt, then correct
	valid, remaining, err := svc.ValidateVerificationCode(1, uint64(payment.ID), "payment", "000000")
	assert.False(t, valid)
	assert.NoError(t, err)
	assert.Equal(t, 2, remaining)

	// Now provide the correct code
	valid, _, err = svc.ValidateVerificationCode(1, uint64(payment.ID), "payment", "123456")
	assert.True(t, valid)
	assert.NoError(t, err)

	// Payment should still be "pending" (not rejected)
	updatedPayment, err := paymentRepo.GetByID(uint64(payment.ID))
	require.NoError(t, err)
	assert.Equal(t, "pending", updatedPayment.Status)
}
