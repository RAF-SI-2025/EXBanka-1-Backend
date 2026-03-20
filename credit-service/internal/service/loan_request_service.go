package service

import (
	"context"
	"fmt"
	"time"

	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
)

var validLoanTypes = map[string]bool{
	"cash": true, "housing": true, "auto": true, "refinancing": true, "student": true,
}

var validInterestTypes = map[string]bool{
	"fixed": true, "variable": true,
}

var allowedRepaymentPeriods = map[string][]int{
	"cash":        {12, 24, 36, 48, 60, 72, 84},
	"housing":     {60, 120, 180, 240, 300, 360},
	"auto":        {12, 24, 36, 48, 60, 72, 84},
	"refinancing": {12, 24, 36, 48, 60, 72, 84},
	"student":     {12, 24, 36, 48, 60, 72, 84},
}

func validateRepaymentPeriod(loanType string, period int) error {
	allowed, ok := allowedRepaymentPeriods[loanType]
	if !ok {
		return fmt.Errorf("unknown loan type: %s", loanType)
	}
	for _, a := range allowed {
		if period == a {
			return nil
		}
	}
	return fmt.Errorf("repayment period %d months is not allowed for %s loans; allowed: %v", period, loanType, allowed)
}

type LoanRequestService struct {
	repo           *repository.LoanRequestRepository
	loanRepo       *repository.LoanRepository
	installRepo    *repository.InstallmentRepository
	limitClient    userpb.EmployeeLimitServiceClient
	rateConfigSvc  *RateConfigService
}

func NewLoanRequestService(
	repo *repository.LoanRequestRepository,
	loanRepo *repository.LoanRepository,
	installRepo *repository.InstallmentRepository,
	limitClient userpb.EmployeeLimitServiceClient,
	rateConfigSvc *RateConfigService,
) *LoanRequestService {
	return &LoanRequestService{repo: repo, loanRepo: loanRepo, installRepo: installRepo, limitClient: limitClient, rateConfigSvc: rateConfigSvc}
}

func (s *LoanRequestService) CreateLoanRequest(req *model.LoanRequest) error {
	if !validLoanTypes[req.LoanType] {
		return fmt.Errorf("loan type must be one of: cash, housing, auto, refinancing, student; got: %s", req.LoanType)
	}
	if !validInterestTypes[req.InterestType] {
		return fmt.Errorf("interest type must be one of: fixed, variable; got: %s", req.InterestType)
	}
	if req.Amount.IsNegative() || req.Amount.IsZero() {
		return fmt.Errorf("loan request amount must be greater than 0; got: %s (loan_type=%s, account=%s)",
			req.Amount.StringFixed(2), req.LoanType, req.AccountNumber)
	}
	if err := validateRepaymentPeriod(req.LoanType, req.RepaymentPeriod); err != nil {
		return err
	}
	if err := s.repo.Create(req); err != nil {
		return fmt.Errorf("failed to save loan request for account %s (loan_type=%s, amount=%s): %v",
			req.AccountNumber, req.LoanType, req.Amount.StringFixed(2), err)
	}
	return nil
}

func (s *LoanRequestService) GetLoanRequest(id uint64) (*model.LoanRequest, error) {
	req, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve loan request %d: %v", id, err)
	}
	return req, nil
}

func (s *LoanRequestService) ListLoanRequests(loanTypeFilter, accountFilter, statusFilter string, clientID uint64, page, pageSize int) ([]model.LoanRequest, int64, error) {
	requests, total, err := s.repo.List(loanTypeFilter, accountFilter, statusFilter, clientID, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list loan requests (loan_type=%s, account=%s, status=%s, client_id=%d, page=%d): %v",
			loanTypeFilter, accountFilter, statusFilter, clientID, page, err)
	}
	return requests, total, nil
}

func (s *LoanRequestService) ApproveLoanRequest(requestID uint64, employeeID uint64) (*model.Loan, error) {
	req, err := s.repo.GetByID(requestID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve loan request %d for approval: %v", requestID, err)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("loan request %d is already %s; only pending requests can be approved", requestID, req.Status)
	}

	// Check employee MaxLoanApprovalAmount limit
	if employeeID > 0 && s.limitClient != nil {
		limits, limErr := s.limitClient.GetEmployeeLimits(context.Background(), &userpb.EmployeeLimitRequest{EmployeeId: int64(employeeID)})
		if limErr == nil && limits.MaxLoanApprovalAmount != "" && limits.MaxLoanApprovalAmount != "0" {
			maxAmount, parseErr := decimal.NewFromString(limits.MaxLoanApprovalAmount)
			if parseErr == nil && maxAmount.IsPositive() && req.Amount.GreaterThan(maxAmount) {
				return nil, fmt.Errorf("loan request %d: loan amount %s exceeds employee %d approval limit of %s (loan_type=%s, account=%s)",
					requestID, req.Amount.StringFixed(2), employeeID, maxAmount.StringFixed(2), req.LoanType, req.AccountNumber)
			}
		}
	}

	nominalRate, rateErr := s.rateConfigSvc.GetNominalRate(req.LoanType, req.InterestType, req.Amount)
	if rateErr != nil {
		return nil, fmt.Errorf("failed to determine interest rate for loan request %d (loan_type=%s, interest_type=%s, amount=%s): %v",
			requestID, req.LoanType, req.InterestType, req.Amount.StringFixed(2), rateErr)
	}
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
		return nil, fmt.Errorf("failed to create loan account entry for request %d (loan_type=%s, amount=%s, account=%s): %v",
			requestID, req.LoanType, req.Amount.StringFixed(2), req.AccountNumber, err)
	}

	// Create installment schedule
	startDateStr := now.Format("2006-01-02")
	installments := CreateInstallmentSchedule(req.Amount, nominalRate, req.RepaymentPeriod, req.CurrencyCode, startDateStr)
	for i := range installments {
		installments[i].LoanID = loan.ID
	}
	if err := s.installRepo.CreateBatch(installments); err != nil {
		return nil, fmt.Errorf("failed to create installment schedule for loan request %d, loan %d (amount=%s, period=%d months): %v",
			requestID, loan.ID, req.Amount.StringFixed(2), req.RepaymentPeriod, err)
	}

	// Update loan request status
	req.Status = "approved"
	if err := s.repo.Update(req); err != nil {
		return nil, fmt.Errorf("failed to update loan request %d status to approved (loan %d already created): %v",
			requestID, loan.ID, err)
	}

	return loan, nil
}

func (s *LoanRequestService) RejectLoanRequest(requestID uint64) (*model.LoanRequest, error) {
	req, err := s.repo.GetByID(requestID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve loan request %d for rejection: %v", requestID, err)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("loan request %d is already %s; only pending requests can be rejected", requestID, req.Status)
	}
	req.Status = "rejected"
	if err := s.repo.Update(req); err != nil {
		return nil, fmt.Errorf("failed to update loan request %d status to rejected (loan_type=%s, amount=%s, account=%s): %v",
			requestID, req.LoanType, req.Amount.StringFixed(2), req.AccountNumber, err)
	}
	return req, nil
}
