package service

import (
	"context"
	"errors"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

type TransferService struct {
	transferRepo *repository.TransferRepository
}

func NewTransferService(transferRepo *repository.TransferRepository) *TransferService {
	return &TransferService{transferRepo: transferRepo}
}

// ValidateTransfer checks that a transfer has distinct accounts and a positive amount.
func ValidateTransfer(from, to string, amount float64) error {
	if from == to {
		return errors.New("from and to accounts must be different")
	}
	if amount <= 0 {
		return errors.New("amount must be positive")
	}
	return nil
}

func (s *TransferService) CreateTransfer(ctx context.Context, transfer *model.Transfer) error {
	if err := ValidateTransfer(transfer.FromAccountNumber, transfer.ToAccountNumber, transfer.InitialAmount); err != nil {
		return err
	}
	transfer.Commission = CalculatePaymentCommission(transfer.InitialAmount)
	transfer.FinalAmount = transfer.InitialAmount + transfer.Commission
	transfer.Timestamp = time.Now()

	return s.transferRepo.Create(transfer)
}

func (s *TransferService) GetTransfer(id uint64) (*model.Transfer, error) {
	return s.transferRepo.GetByID(id)
}

func (s *TransferService) ListTransfersByClient(clientID uint64, page, pageSize int) ([]model.Transfer, int64, error) {
	return s.transferRepo.ListByClient(clientID, page, pageSize)
}
