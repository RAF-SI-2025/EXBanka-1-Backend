package service

import (
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

type LoanService struct {
	loanRepo *repository.LoanRepository
}

func NewLoanService(loanRepo *repository.LoanRepository) *LoanService {
	return &LoanService{loanRepo: loanRepo}
}

func (s *LoanService) GetLoan(id uint64) (*model.Loan, error) {
	return s.loanRepo.GetByID(id)
}

func (s *LoanService) ListLoansByClient(clientID uint64, page, pageSize int) ([]model.Loan, int64, error) {
	return s.loanRepo.List(clientID, page, pageSize)
}

func (s *LoanService) ListAllLoans(loanTypeFilter, accountFilter, statusFilter string, page, pageSize int) ([]model.Loan, int64, error) {
	return s.loanRepo.ListAll(loanTypeFilter, accountFilter, statusFilter, page, pageSize)
}
