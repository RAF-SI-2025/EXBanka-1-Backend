package service

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

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
	loan, err := s.loanRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("GetLoan(id=%d): %w", id, ErrLoanNotFound)
		}
		return nil, fmt.Errorf("GetLoan(id=%d): %v: %w", id, err, ErrLoanLookup)
	}
	return loan, nil
}

func (s *LoanService) ListLoansByClient(clientID uint64, page, pageSize int) ([]model.Loan, int64, error) {
	loans, total, err := s.loanRepo.List(clientID, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("ListLoansByClient(client_id=%d, page=%d): %v: %w",
			clientID, page, err, ErrLoanLookup)
	}
	return loans, total, nil
}

func (s *LoanService) ListAllLoans(loanTypeFilter, accountFilter, statusFilter string, page, pageSize int) ([]model.Loan, int64, error) {
	loans, total, err := s.loanRepo.ListAll(loanTypeFilter, accountFilter, statusFilter, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("ListAllLoans(loan_type=%s, account=%s, status=%s, page=%d): %v: %w",
			loanTypeFilter, accountFilter, statusFilter, page, err, ErrLoanLookup)
	}
	return loans, total, nil
}
