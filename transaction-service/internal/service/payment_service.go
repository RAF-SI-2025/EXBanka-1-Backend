package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
)

// PaymentRepo abstracts the PaymentRepository for testing.
type PaymentRepo interface {
	Create(payment *model.Payment) error
	GetByID(id uint64) (*model.Payment, error)
	GetByIdempotencyKey(key string) (*model.Payment, error)
	UpdateStatus(id uint64, status string) error
	UpdateStatusWithReason(id uint64, status, reason string) error
	ListByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error)
}

type PaymentService struct {
	paymentRepo    PaymentRepo
	accountClient  accountpb.AccountServiceClient
	feeSvc         *FeeService
	producer       *kafka.Producer
	bankRSDAccount string // account number of bank's RSD account
}

func NewPaymentService(paymentRepo PaymentRepo, accountClient accountpb.AccountServiceClient, feeSvc *FeeService, producer *kafka.Producer, bankRSDAccount string) *PaymentService {
	return &PaymentService{
		paymentRepo:    paymentRepo,
		accountClient:  accountClient,
		feeSvc:         feeSvc,
		producer:       producer,
		bankRSDAccount: bankRSDAccount,
	}
}

// publishPaymentFailed publishes a payment-failed Kafka event (best-effort; errors are only logged).
func (s *PaymentService) publishPaymentFailed(ctx context.Context, payment *model.Payment, reason string) {
	if s.producer == nil {
		return
	}
	msg := kafkamsg.PaymentFailedMessage{
		PaymentID:         payment.ID,
		FromAccountNumber: payment.FromAccountNumber,
		ToAccountNumber:   payment.ToAccountNumber,
		Amount:            payment.FinalAmount.StringFixed(4),
		FailureReason:     reason,
	}
	if err := s.producer.PublishPaymentFailed(ctx, msg); err != nil {
		log.Printf("PaymentService: failed to publish payment-failed event for payment %d: %v", payment.ID, err)
	}
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
			return fmt.Errorf("idempotency check failed for key %q: %w", payment.IdempotencyKey, err)
		}
	}

	// 2. Validate amount is positive
	if payment.InitialAmount.IsNegative() || payment.InitialAmount.IsZero() {
		return fmt.Errorf("payment amount must be positive, got %s", payment.InitialAmount.StringFixed(4))
	}

	currency := payment.CurrencyCode
	if currency == "" {
		currency = "RSD"
	}
	commission, err := s.feeSvc.CalculateFee(payment.InitialAmount, "payment", currency)
	if err != nil {
		return fmt.Errorf("fee calculation failed for payment of %s %s from account %s: %w",
			payment.InitialAmount.StringFixed(4), currency, payment.FromAccountNumber, err)
	}
	payment.Commission = commission
	payment.FinalAmount = payment.InitialAmount.Add(payment.Commission)
	payment.Status = "processing"
	payment.Timestamp = time.Now()

	if err := s.paymentRepo.Create(payment); err != nil {
		return fmt.Errorf("failed to persist payment from %s to %s: %w",
			payment.FromAccountNumber, payment.ToAccountNumber, err)
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
				reason := fmt.Sprintf("limit_exceeded: daily spending limit would be exceeded on account %s: current daily spending %s, attempted %s, daily limit %s",
					payment.FromAccountNumber, dailySpending.StringFixed(4), totalDebit.StringFixed(4), dailyLimit.StringFixed(4))
				_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
				payment.Status = "failed"
				payment.FailureReason = reason
				s.publishPaymentFailed(ctx, payment, reason)
				return errors.New(reason)
			}
			if !monthlyLimit.IsZero() && monthlySpending.Add(totalDebit).GreaterThan(monthlyLimit) {
				reason := fmt.Sprintf("limit_exceeded: monthly spending limit would be exceeded on account %s: current monthly spending %s, attempted %s, monthly limit %s",
					payment.FromAccountNumber, monthlySpending.StringFixed(4), totalDebit.StringFixed(4), monthlyLimit.StringFixed(4))
				_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
				payment.Status = "failed"
				payment.FailureReason = reason
				s.publishPaymentFailed(ctx, payment, reason)
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
			reason := fmt.Sprintf("debit failed on account %s for amount %s %s: %v",
				payment.FromAccountNumber, totalDebit.StringFixed(4), currency, err)
			_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
			payment.Status = "failed"
			payment.FailureReason = reason
			s.publishPaymentFailed(ctx, payment, reason)
			return fmt.Errorf("failed to debit sender account %s for %s %s: %w",
				payment.FromAccountNumber, totalDebit.StringFixed(4), currency, err)
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
			reason := fmt.Sprintf("credit failed on recipient account %s for amount %s %s (debit of %s on %s reversed): %v",
				payment.ToAccountNumber, payment.InitialAmount.StringFixed(4), currency,
				totalDebit.StringFixed(4), payment.FromAccountNumber, err)
			_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
			payment.Status = "failed"
			payment.FailureReason = reason
			s.publishPaymentFailed(ctx, payment, reason)
			return fmt.Errorf("failed to credit recipient account %s for %s %s: %w",
				payment.ToAccountNumber, payment.InitialAmount.StringFixed(4), currency, err)
		}
	}

	// 6. Credit commission to bank's own RSD account (best-effort)
	if s.bankRSDAccount != "" && payment.Commission.IsPositive() {
		commissionAmt := payment.Commission.StringFixed(4)
		_ = shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   s.bankRSDAccount,
				Amount:          commissionAmt,
				UpdateAvailable: true,
			})
			return e
		})
	}

	// 7. Mark payment completed
	now := time.Now()
	payment.CompletedAt = &now
	if err := s.paymentRepo.UpdateStatus(payment.ID, "completed"); err != nil {
		return fmt.Errorf("failed to mark payment %d as completed (from %s to %s): %w",
			payment.ID, payment.FromAccountNumber, payment.ToAccountNumber, err)
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
