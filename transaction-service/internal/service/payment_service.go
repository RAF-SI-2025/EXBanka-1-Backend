package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

type PaymentService struct {
	paymentRepo   *repository.PaymentRepository
	accountClient accountpb.AccountServiceClient
}

func NewPaymentService(paymentRepo *repository.PaymentRepository, accountClient accountpb.AccountServiceClient) *PaymentService {
	return &PaymentService{paymentRepo: paymentRepo, accountClient: accountClient}
}

// CalculatePaymentCommission returns 0.1% commission for amounts >= 1000, else 0.
func CalculatePaymentCommission(amount decimal.Decimal) decimal.Decimal {
	threshold := decimal.NewFromInt(1000)
	if amount.GreaterThanOrEqual(threshold) {
		return amount.Mul(decimal.NewFromFloat(0.001)) // 0.1%
	}
	return decimal.Zero
}

func (s *PaymentService) CreatePayment(ctx context.Context, payment *model.Payment) error {
	// 1. Idempotency check: return existing payment if key already used
	if payment.IdempotencyKey != "" {
		existing, err := s.paymentRepo.GetByIdempotencyKey(payment.IdempotencyKey)
		if err == nil {
			*payment = *existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("idempotency check failed: %w", err)
		}
	}

	// 2. Validate amount is positive
	if payment.InitialAmount.IsNegative() || payment.InitialAmount.IsZero() {
		return errors.New("payment amount must be positive")
	}

	payment.Commission = CalculatePaymentCommission(payment.InitialAmount)
	payment.FinalAmount = payment.InitialAmount.Add(payment.Commission)
	payment.Status = "processing"
	payment.Timestamp = time.Now()

	if err := s.paymentRepo.Create(payment); err != nil {
		return err
	}

	totalDebit := payment.FinalAmount

	// 3. Spending limit enforcement
	if s.accountClient != nil {
		var acctResp *accountpb.AccountResponse
		if err := shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			var e error
			acctResp, e = s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
				AccountNumber: payment.FromAccountNumber,
			})
			return e
		}); err == nil && acctResp != nil {
			dailyLimit, _ := decimal.NewFromString(acctResp.GetDailyLimit())
			monthlyLimit, _ := decimal.NewFromString(acctResp.GetMonthlyLimit())
			dailySpending, _ := decimal.NewFromString(acctResp.GetDailySpending())
			monthlySpending, _ := decimal.NewFromString(acctResp.GetMonthlySpending())

			if !dailyLimit.IsZero() && dailySpending.Add(totalDebit).GreaterThan(dailyLimit) {
				reason := "limit_exceeded: daily spending limit would be exceeded"
				_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
				payment.Status = "failed"
				payment.FailureReason = reason
				return errors.New(reason)
			}
			if !monthlyLimit.IsZero() && monthlySpending.Add(totalDebit).GreaterThan(monthlyLimit) {
				reason := "limit_exceeded: monthly spending limit would be exceeded"
				_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
				payment.Status = "failed"
				payment.FailureReason = reason
				return errors.New(reason)
			}
		}
	}

	// 4. Debit sender account
	if s.accountClient != nil {
		debitAmt := totalDebit.Neg().StringFixed(4)
		if err := shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber: payment.FromAccountNumber,
				Amount:        debitAmt,
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			reason := fmt.Sprintf("debit failed: %v", err)
			_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
			payment.Status = "failed"
			payment.FailureReason = reason
			return fmt.Errorf("failed to debit sender account: %w", err)
		}
	}

	// 5. Credit recipient account
	if s.accountClient != nil {
		creditAmt := payment.InitialAmount.StringFixed(4)
		if err := shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber: payment.ToAccountNumber,
				Amount:        creditAmt,
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			// 6. Compensating transaction: reverse the debit
			reverseAmt := totalDebit.StringFixed(4)
			_ = shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber: payment.FromAccountNumber,
					Amount:        reverseAmt,
					UpdateAvailable: true,
				})
				return e
			})
			reason := fmt.Sprintf("credit failed (debit reversed): %v", err)
			_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
			payment.Status = "failed"
			payment.FailureReason = reason
			return fmt.Errorf("failed to credit recipient account: %w", err)
		}
	}

	// 7. Mark payment completed
	now := time.Now()
	payment.CompletedAt = &now
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
