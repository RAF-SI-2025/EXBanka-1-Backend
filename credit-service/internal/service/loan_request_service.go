package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

var validLoanTypes = map[string]bool{
	"cash": true, "housing": true, "auto": true, "refinancing": true, "student": true,
}

var validInterestTypes = map[string]bool{
	"fixed": true, "variable": true,
}

type LoanRequestService struct {
	repo        *repository.LoanRequestRepository
	loanRepo    *repository.LoanRepository
	installRepo *repository.InstallmentRepository
}

func NewLoanRequestService(
	repo *repository.LoanRequestRepository,
	loanRepo *repository.LoanRepository,
	installRepo *repository.InstallmentRepository,
) *LoanRequestService {
	return &LoanRequestService{repo: repo, loanRepo: loanRepo, installRepo: installRepo}
}

func (s *LoanRequestService) CreateLoanRequest(req *model.LoanRequest) error {
	if !validLoanTypes[req.LoanType] {
		return fmt.Errorf("invalid loan type: %s", req.LoanType)
	}
	if !validInterestTypes[req.InterestType] {
		return fmt.Errorf("invalid interest type: %s", req.InterestType)
	}
	if req.Amount.IsNegative() || req.Amount.IsZero() {
		return errors.New("amount must be greater than 0")
	}
	if req.RepaymentPeriod <= 0 {
		return errors.New("repayment period must be greater than 0")
	}
	return s.repo.Create(req)
}

func (s *LoanRequestService) GetLoanRequest(id uint64) (*model.LoanRequest, error) {
	return s.repo.GetByID(id)
}

func (s *LoanRequestService) ListLoanRequests(loanTypeFilter, accountFilter, statusFilter string, clientID uint64, page, pageSize int) ([]model.LoanRequest, int64, error) {
	return s.repo.List(loanTypeFilter, accountFilter, statusFilter, clientID, page, pageSize)
}

func (s *LoanRequestService) ApproveLoanRequest(requestID uint64) (*model.Loan, error) {
	req, err := s.repo.GetByID(requestID)
	if err != nil {
		return nil, err
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("loan request is not in pending state: %s", req.Status)
	}

	nominalRate := GetNominalInterestRate(req.LoanType, req.InterestType)
	effectiveRate := CalculateEffectiveInterestRate(nominalRate, 12)
	monthlyInstallment := CalculateMonthlyInstallment(req.Amount, nominalRate, req.RepaymentPeriod)

	now := time.Now()
	maturityDate := now.AddDate(0, req.RepaymentPeriod, 0)
	nextInstallmentDate := now.AddDate(0, 1, 0)

	loan := &model.Loan{
		LoanNumber:            s.loanRepo.GenerateLoanNumber(),
		LoanType:              req.LoanType,
		AccountNumber:         req.AccountNumber,
		Amount:                req.Amount,
		RepaymentPeriod:       req.RepaymentPeriod,
		NominalInterestRate:   nominalRate,
		EffectiveInterestRate: effectiveRate,
		ContractDate:          now,
		MaturityDate:          maturityDate,
		NextInstallmentAmount: monthlyInstallment,
		NextInstallmentDate:   nextInstallmentDate,
		RemainingDebt:         req.Amount,
		CurrencyCode:          req.CurrencyCode,
		Status:                "approved",
		InterestType:          req.InterestType,
		ClientID:              req.ClientID,
	}

	if err := s.loanRepo.Create(loan); err != nil {
		return nil, err
	}

	// Create installment schedule
	startDateStr := now.Format("2006-01-02")
	installments := CreateInstallmentSchedule(req.Amount, nominalRate, req.RepaymentPeriod, req.CurrencyCode, startDateStr)
	for i := range installments {
		installments[i].LoanID = loan.ID
	}
	if err := s.installRepo.CreateBatch(installments); err != nil {
		return nil, err
	}

	// Update loan request status
	req.Status = "approved"
	if err := s.repo.Update(req); err != nil {
		return nil, err
	}

	return loan, nil
}

func (s *LoanRequestService) RejectLoanRequest(requestID uint64) (*model.LoanRequest, error) {
	req, err := s.repo.GetByID(requestID)
	if err != nil {
		return nil, err
	}
	req.Status = "rejected"
	if err := s.repo.Update(req); err != nil {
		return nil, err
	}
	return req, nil
}
