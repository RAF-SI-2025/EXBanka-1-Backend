package service

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

type InstallmentService struct {
	installRepo *repository.InstallmentRepository
}

func NewInstallmentService(installRepo *repository.InstallmentRepository) *InstallmentService {
	return &InstallmentService{installRepo: installRepo}
}

// CreateInstallmentSchedule creates a slice of installments for a loan (does not persist them)
func CreateInstallmentSchedule(amount, annualRate decimal.Decimal, months int, currency string, startDateStr string) []model.Installment {
	monthlyPayment := CalculateMonthlyInstallment(amount, annualRate, months)

	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		startDate = time.Now()
	}

	installments := make([]model.Installment, months)
	for i := 0; i < months; i++ {
		dueDate := startDate.AddDate(0, i+1, 0)
		installments[i] = model.Installment{
			Amount:       monthlyPayment,
			InterestRate: annualRate,
			CurrencyCode: currency,
			ExpectedDate: dueDate,
			Status:       "unpaid",
		}
	}
	return installments
}

func (s *InstallmentService) GetInstallmentsByLoan(loanID uint64) ([]model.Installment, error) {
	insts, err := s.installRepo.ListByLoan(loanID)
	if err != nil {
		return nil, fmt.Errorf("GetInstallmentsByLoan(loan_id=%d): %v: %w", loanID, err, ErrInstallmentLookup)
	}
	return insts, nil
}

func (s *InstallmentService) MarkInstallmentPaid(installmentID uint64) error {
	return s.installRepo.MarkPaid(installmentID)
}

func (s *InstallmentService) MarkOverdueInstallments() error {
	return s.installRepo.MarkOverdue()
}

func (s *InstallmentService) GetDueInstallments() ([]model.Installment, error) {
	return s.installRepo.GetDueInstallments()
}
