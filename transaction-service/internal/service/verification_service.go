package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

const maxAttempts = 3

type VerificationService struct {
	repo         *repository.VerificationCodeRepository
	paymentRepo  *repository.PaymentRepository
	transferRepo *repository.TransferRepository
}

func NewVerificationService(
	repo *repository.VerificationCodeRepository,
	paymentRepo *repository.PaymentRepository,
	transferRepo *repository.TransferRepository,
) *VerificationService {
	return &VerificationService{
		repo:         repo,
		paymentRepo:  paymentRepo,
		transferRepo: transferRepo,
	}
}

// GenerateCode returns a 6-digit random number as a zero-padded string.
func GenerateCode() string {
	n := rand.Intn(1000000)
	return fmt.Sprintf("%06d", n)
}

// CreateVerificationCode generates a new 6-digit code, stores it with a 5-minute expiry,
// and returns the VerificationCode record and the plain code.
func (s *VerificationService) CreateVerificationCode(ctx context.Context, clientID, transactionID uint64, txType string) (*model.VerificationCode, string, error) {
	code := GenerateCode()
	vc := &model.VerificationCode{
		ClientID:        clientID,
		TransactionID:   transactionID,
		TransactionType: txType,
		Code:            code,
		ExpiresAt:       time.Now().Add(5 * time.Minute),
		Attempts:        0,
		Used:            false,
	}
	if err := s.repo.Create(vc); err != nil {
		return nil, "", err
	}
	return vc, code, nil
}

// ValidateVerificationCode checks expiry, max attempts, and correctness.
// Returns (valid, remainingAttempts, error).
func (s *VerificationService) ValidateVerificationCode(clientID, transactionID uint64, txType, code string) (bool, int, error) {
	// Find the most recent unused code for this client+transaction+type
	vc, err := s.repo.GetByClientAndTransaction(clientID, transactionID, txType)
	if err != nil {
		return false, 0, err
	}

	if vc.Used {
		return false, 0, errors.New("verification code already used")
	}
	if time.Now().After(vc.ExpiresAt) {
		return false, 0, errors.New("verification code expired")
	}
	if vc.Attempts >= maxAttempts {
		// Cancel the associated transaction
		s.cancelTransaction(vc.TransactionID, vc.TransactionType)
		return false, 0, fmt.Errorf("verification code expired after %d failed attempts; transaction cancelled", maxAttempts)
	}

	if err := s.repo.IncrementAttempts(vc.ID); err != nil {
		return false, 0, err
	}
	vc.Attempts++

	remaining := maxAttempts - vc.Attempts
	if vc.Code != code {
		if remaining == 0 {
			// Max attempts now reached - cancel transaction
			s.cancelTransaction(vc.TransactionID, vc.TransactionType)
			return false, 0, fmt.Errorf("verification code expired after %d failed attempts; transaction cancelled", maxAttempts)
		}
		return false, remaining, nil
	}

	if err := s.repo.MarkUsed(vc.ID); err != nil {
		return false, remaining, err
	}
	return true, remaining, nil
}

// cancelTransaction sets the associated transaction's status to "rejected".
func (s *VerificationService) cancelTransaction(transactionID uint64, txType string) {
	var err error
	switch txType {
	case "payment":
		err = s.paymentRepo.UpdateStatus(transactionID, "rejected")
	case "transfer":
		err = s.transferRepo.UpdateStatus(transactionID, "rejected")
	}
	if err != nil {
		log.Printf("warn: failed to cancel %s transaction %d after max verification attempts: %v", txType, transactionID, err)
	}
}
