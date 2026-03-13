package service

import (
	"context"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

type PaymentService struct {
	paymentRepo *repository.PaymentRepository
}

func NewPaymentService(paymentRepo *repository.PaymentRepository) *PaymentService {
	return &PaymentService{paymentRepo: paymentRepo}
}

// CalculatePaymentCommission returns 0.1% commission for amounts >= 1000, else 0.
func CalculatePaymentCommission(amount float64) float64 {
	if amount >= 1000 {
		return amount * 0.001
	}
	return 0
}

func (s *PaymentService) CreatePayment(ctx context.Context, payment *model.Payment) error {
	payment.Commission = CalculatePaymentCommission(payment.InitialAmount)
	payment.FinalAmount = payment.InitialAmount + payment.Commission
	payment.Status = "processing"
	payment.Timestamp = time.Now()

	if err := s.paymentRepo.Create(payment); err != nil {
		return err
	}

	// Mark as completed
	if err := s.paymentRepo.UpdateStatus(payment.ID, "completed"); err != nil {
		return err
	}
	payment.Status = "completed"
	return nil
}

func (s *PaymentService) GetPayment(id uint64) (*model.Payment, error) {
	return s.paymentRepo.GetByID(id)
}

func (s *PaymentService) ListPaymentsByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error) {
	return s.paymentRepo.ListByAccount(accountNumber, dateFrom, dateTo, statusFilter, amountMin, amountMax, page, pageSize)
}
