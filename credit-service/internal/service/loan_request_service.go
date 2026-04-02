package service

import (
	"context"
	"fmt"
	"log"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
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
	repo          *repository.LoanRequestRepository
	loanRepo      *repository.LoanRepository
	installRepo   *repository.InstallmentRepository
	limitClient   userpb.EmployeeLimitServiceClient
	accountClient accountpb.AccountServiceClient
	rateConfigSvc *RateConfigService
	db            *gorm.DB
}

func NewLoanRequestService(
	repo *repository.LoanRequestRepository,
	loanRepo *repository.LoanRepository,
	installRepo *repository.InstallmentRepository,
	limitClient userpb.EmployeeLimitServiceClient,
	accountClient accountpb.AccountServiceClient,
	rateConfigSvc *RateConfigService,
	db *gorm.DB,
) *LoanRequestService {
	return &LoanRequestService{repo: repo, loanRepo: loanRepo, installRepo: installRepo, limitClient: limitClient, accountClient: accountClient, rateConfigSvc: rateConfigSvc, db: db}
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
	// Validate loan currency matches account currency
	if s.accountClient != nil {
		account, err := s.accountClient.GetAccountByNumber(context.Background(), &accountpb.GetAccountByNumberRequest{
			AccountNumber: req.AccountNumber,
		})
		if err != nil {
			return fmt.Errorf("failed to verify account %s: %w", req.AccountNumber, err)
		}
		if account.CurrencyCode != req.CurrencyCode {
			return fmt.Errorf("loan currency (%s) must match account currency (%s)", req.CurrencyCode, account.CurrencyCode)
		}
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

func (s *LoanRequestService) ApproveLoanRequest(ctx context.Context, requestID uint64, employeeID uint64) (*model.Loan, error) {
	// Pre-check: read loan request to validate before taking any locks.
	// A second authoritative check happens inside the transaction.
	req, err := s.repo.GetByID(requestID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve loan request %d for approval: %v", requestID, err)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("loan request %d is already %s; only pending requests can be approved", requestID, req.Status)
	}

	// Check employee MaxLoanApprovalAmount limit (advisory — gRPC call cannot be held inside a DB TX).
	if employeeID > 0 && s.limitClient != nil {
		limits, limErr := s.limitClient.GetEmployeeLimits(ctx, &userpb.EmployeeLimitRequest{EmployeeId: int64(employeeID)})
		if limErr == nil && limits.MaxLoanApprovalAmount != "" && limits.MaxLoanApprovalAmount != "0" {
			maxAmount, parseErr := decimal.NewFromString(limits.MaxLoanApprovalAmount)
			if parseErr == nil && maxAmount.IsPositive() && req.Amount.GreaterThan(maxAmount) {
				return nil, fmt.Errorf("loan request %d: loan amount %s exceeds employee %d approval limit of %s (loan_type=%s, account=%s)",
					requestID, req.Amount.StringFixed(2), employeeID, maxAmount.StringFixed(2), req.LoanType, req.AccountNumber)
			}
		}
	}

	// Compute rates outside the TX (pure calculation, no I/O).
	baseRate, bankMargin, nominalRate, rateErr := s.rateConfigSvc.GetNominalRateComponents(req.LoanType, req.InterestType, req.Amount)
	if rateErr != nil {
		return nil, fmt.Errorf("failed to determine interest rate for loan request %d (loan_type=%s, interest_type=%s, amount=%s): %v",
			requestID, req.LoanType, req.InterestType, req.Amount.StringFixed(2), rateErr)
	}

	var loan *model.Loan
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// Re-read with SELECT FOR UPDATE to prevent concurrent double-approval.
		locked, e := s.repo.GetByIDForUpdate(tx, requestID)
		if e != nil {
			return fmt.Errorf("failed to retrieve loan request %d for approval: %v", requestID, e)
		}
		if locked.Status != "pending" {
			return fmt.Errorf("loan request %d is already %s; only pending requests can be approved", requestID, locked.Status)
		}

		effectiveRate := CalculateEffectiveInterestRate(nominalRate, 12)
		monthlyInstallment := CalculateMonthlyInstallment(locked.Amount, nominalRate, locked.RepaymentPeriod)

		now := time.Now()
		loan = &model.Loan{
			LoanNumber:            s.loanRepo.GenerateLoanNumber(),
			LoanType:              locked.LoanType,
			AccountNumber:         locked.AccountNumber,
			Amount:                locked.Amount,
			RepaymentPeriod:       locked.RepaymentPeriod,
			NominalInterestRate:   nominalRate,
			EffectiveInterestRate: effectiveRate,
			ContractDate:          now,
			MaturityDate:          now.AddDate(0, locked.RepaymentPeriod, 0),
			NextInstallmentAmount: monthlyInstallment,
			NextInstallmentDate:   now.AddDate(0, 1, 0),
			RemainingDebt:         locked.Amount,
			CurrencyCode:          locked.CurrencyCode,
			Status:                "approved",
			InterestType:          locked.InterestType,
			BaseRate:              baseRate,
			BankMargin:            bankMargin,
			CurrentRate:           nominalRate,
			ClientID:              locked.ClientID,
		}

		if e := tx.Create(loan).Error; e != nil {
			return fmt.Errorf("failed to create loan for request %d (loan_type=%s, amount=%s, account=%s): %v",
				requestID, locked.LoanType, locked.Amount.StringFixed(2), locked.AccountNumber, e)
		}

		startDateStr := now.Format("2006-01-02")
		installments := CreateInstallmentSchedule(locked.Amount, nominalRate, locked.RepaymentPeriod, locked.CurrencyCode, startDateStr)
		for i := range installments {
			installments[i].LoanID = loan.ID
		}
		if e := tx.Create(&installments).Error; e != nil {
			return fmt.Errorf("failed to create installment schedule for loan request %d, loan %d (amount=%s, period=%d months): %v",
				requestID, loan.ID, locked.Amount.StringFixed(2), locked.RepaymentPeriod, e)
		}

		locked.Status = "approved"
		if e := tx.Save(locked).Error; e != nil {
			return fmt.Errorf("failed to update loan request %d status to approved: %v", requestID, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Disbursement: credit the loan amount to the borrower's account (outside TX — gRPC cannot be held inside a DB TX).
	if s.accountClient == nil {
		return loan, nil
	}
	_, disburseErr := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   loan.AccountNumber,
		Amount:          loan.Amount.StringFixed(4),
		UpdateAvailable: true,
	})
	if disburseErr != nil {
		loan.Status = "disbursement_failed"
	} else {
		loan.Status = "active"
	}
	if updateErr := s.loanRepo.Update(loan); updateErr != nil {
		log.Printf("ApproveLoanRequest: failed to update loan %d status to %s after disbursement: %v", loan.ID, loan.Status, updateErr)
	}
	return loan, nil
}

func (s *LoanRequestService) RejectLoanRequest(requestID uint64) (*model.LoanRequest, error) {
	var req *model.LoanRequest
	err := s.db.Transaction(func(tx *gorm.DB) error {
		locked, e := s.repo.GetByIDForUpdate(tx, requestID)
		if e != nil {
			return fmt.Errorf("failed to retrieve loan request %d for rejection: %v", requestID, e)
		}
		if locked.Status != "pending" {
			return fmt.Errorf("loan request %d is already %s; only pending requests can be rejected", requestID, locked.Status)
		}
		locked.Status = "rejected"
		if e := tx.Save(locked).Error; e != nil {
			return fmt.Errorf("failed to update loan request %d status to rejected (loan_type=%s, amount=%s, account=%s): %v",
				requestID, locked.LoanType, locked.Amount.StringFixed(2), locked.AccountNumber, e)
		}
		req = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return req, nil
}
