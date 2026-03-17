package service

import (
	"time"

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
func CreateInstallmentSchedule(amount float64, annualRate float64, months int, currency string, startDateStr string) []model.Installment {
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
	return s.installRepo.ListByLoan(loanID)
}

func (s *InstallmentService) MarkInstallmentPaid(installmentID uint64) error {
	return s.installRepo.MarkPaid(installmentID)
}

func (s *InstallmentService) MarkOverdueInstallments() error {
	return s.installRepo.MarkOverdue()
}
