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
	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// PaymentRepo abstracts the PaymentRepository for testing.
type PaymentRepo interface {
	Create(payment *model.Payment) error
	GetByID(id uint64) (*model.Payment, error)
	GetByIdempotencyKey(key string) (*model.Payment, error)
	UpdateStatus(id uint64, status string) error
	UpdateStatusWithReason(id uint64, status, reason string) error
	ListByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error)
	ListByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Payment, int64, error)
}

// notifier is the narrow slice of the Kafka producer the service uses for
// in-app notification intents. A separate interface (not the concrete
// *kafka.Producer) so unit tests can inject a recording stub.
type notifier interface {
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}

type PaymentService struct {
	paymentRepo    PaymentRepo
	accountClient  accountpb.AccountServiceClient
	feeSvc         *FeeService
	producer       *kafka.Producer
	notifier       notifier
	bankRSDAccount string                        // account number of bank's RSD account
	sagaRepo       *repository.SagaLogRepository // nil-safe: saga logging skipped when nil
	retryConfig    shared.RetryConfig
}

func NewPaymentService(paymentRepo PaymentRepo, accountClient accountpb.AccountServiceClient, feeSvc *FeeService, producer *kafka.Producer, bankRSDAccount string, sagaRepo *repository.SagaLogRepository) *PaymentService {
	s := &PaymentService{
		paymentRepo:    paymentRepo,
		accountClient:  accountClient,
		feeSvc:         feeSvc,
		producer:       producer,
		bankRSDAccount: bankRSDAccount,
		sagaRepo:       sagaRepo,
		retryConfig:    shared.DefaultRetryConfig,
	}
	if producer != nil {
		s.notifier = producer
	}
	return s
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
		return fmt.Errorf("CreatePayment: amount must be positive, got %s: %w", payment.InitialAmount.StringFixed(4), ErrInvalidPayment)
	}

	currency := payment.CurrencyCode
	if currency == "" {
		currency = "RSD"
	}
	commission, err := s.feeSvc.CalculateFee(payment.InitialAmount, "payment", currency)
	if err != nil {
		log.Printf("CreatePayment fee lookup failed: amount=%s currency=%s from=%s err=%v",
			payment.InitialAmount.StringFixed(4), currency, payment.FromAccountNumber, err)
		return fmt.Errorf("CreatePayment: %w", ErrFeeLookupFailed)
	}
	payment.Commission = commission
	payment.FinalAmount = payment.InitialAmount.Add(payment.Commission)
	payment.Timestamp = time.Now()

	totalDebit := payment.FinalAmount

	// 3. Client ownership validation: payments must be between accounts of different clients
	if s.accountClient != nil {
		var fromAccount, toAccount *accountpb.AccountResponse
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			var e error
			fromAccount, e = s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
				AccountNumber: payment.FromAccountNumber,
			})
			return e
		}); err != nil {
			return fmt.Errorf("failed to fetch sender account %s: %w", payment.FromAccountNumber, err)
		}
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			var e error
			toAccount, e = s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
				AccountNumber: payment.ToAccountNumber,
			})
			return e
		}); err != nil {
			return fmt.Errorf("failed to fetch recipient account %s: %w", payment.ToAccountNumber, err)
		}
		if fromAccount.OwnerId == toAccount.OwnerId {
			return fmt.Errorf("payments must be between accounts of different clients; use transfers for same-client transactions")
		}

		// 4. Spending limit pre-check (advisory only — the authoritative check happens
		// atomically inside account-service's UpdateBalance within a FOR UPDATE transaction).
		dailyLimit, _ := decimal.NewFromString(fromAccount.GetDailyLimit())
		monthlyLimit, _ := decimal.NewFromString(fromAccount.GetMonthlyLimit())
		dailySpending, _ := decimal.NewFromString(fromAccount.GetDailySpending())
		monthlySpending, _ := decimal.NewFromString(fromAccount.GetMonthlySpending())

		if !dailyLimit.IsZero() && dailySpending.Add(totalDebit).GreaterThan(dailyLimit) {
			return fmt.Errorf("limit_exceeded: daily spending limit would be exceeded on account %s: current daily spending %s, attempted %s, daily limit %s",
				payment.FromAccountNumber, dailySpending.StringFixed(4), totalDebit.StringFixed(4), dailyLimit.StringFixed(4))
		}
		if !monthlyLimit.IsZero() && monthlySpending.Add(totalDebit).GreaterThan(monthlyLimit) {
			return fmt.Errorf("limit_exceeded: monthly spending limit would be exceeded on account %s: current monthly spending %s, attempted %s, monthly limit %s",
				payment.FromAccountNumber, monthlySpending.StringFixed(4), totalDebit.StringFixed(4), monthlyLimit.StringFixed(4))
		}
	}

	// 5. Save payment in pending_verification status (no balance changes yet)
	payment.Status = "pending_verification"
	if err := s.paymentRepo.Create(payment); err != nil {
		return fmt.Errorf("failed to persist payment from %s to %s: %w",
			payment.FromAccountNumber, payment.ToAccountNumber, err)
	}

	TransactionTotal.WithLabelValues("payment", "created").Inc()
	return nil
}

// ExecutePayment performs the actual balance changes for a payment that has been verified.
// The payment must be in "pending_verification" status.
func (s *PaymentService) ExecutePayment(ctx context.Context, paymentID uint64) error {
	start := time.Now()

	payment, err := s.paymentRepo.GetByID(paymentID)
	if err != nil {
		return fmt.Errorf("payment not found: %w", err)
	}

	// Idempotency: if already completed, nothing to do
	if payment.Status == "completed" {
		return nil
	}

	if payment.Status != "pending_verification" {
		return fmt.Errorf("payment %d is in status %q, expected pending_verification", paymentID, payment.Status)
	}

	// Mark as processing
	if err := s.paymentRepo.UpdateStatus(payment.ID, "processing"); err != nil {
		return fmt.Errorf("failed to mark payment %d as processing: %w", payment.ID, err)
	}
	payment.Status = "processing"

	currency := payment.CurrencyCode
	if currency == "" {
		currency = "RSD"
	}
	totalDebit := payment.FinalAmount

	// Re-check spending limits at execution time
	if s.accountClient != nil {
		var acctResp *accountpb.AccountResponse
		if err := shared.Retry(ctx, s.retryConfig, func() error {
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
				s.publishPaymentFailed(ctx, payment, reason)
				return errors.New(reason)
			}
			if !monthlyLimit.IsZero() && monthlySpending.Add(totalDebit).GreaterThan(monthlyLimit) {
				reason := fmt.Sprintf("limit_exceeded: monthly spending limit would be exceeded on account %s: current monthly spending %s, attempted %s, monthly limit %s",
					payment.FromAccountNumber, monthlySpending.StringFixed(4), totalDebit.StringFixed(4), monthlyLimit.StringFixed(4))
				_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
				s.publishPaymentFailed(ctx, payment, reason)
				return errors.New(reason)
			}
		}
	}

	// Debit sender, credit recipient, and (when commission applies) credit bank — all
	// as saga-logged steps so compensation is automatic if any step fails.
	if s.accountClient != nil {
		paySagaID := fmt.Sprintf("payment-%d", payment.ID)
		steps := []sagaStep{
			{
				name:          sharedsaga.StepDebitSender,
				accountNumber: payment.FromAccountNumber,
				amount:        totalDebit.Neg(),
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber: payment.FromAccountNumber, Amount: totalDebit.Neg().StringFixed(4), UpdateAvailable: true,
							IdempotencyKey: sharedsaga.IdempotencyKey(paySagaID, sharedsaga.StepDebitSender),
						})
						return e
					})
				},
			},
			{
				name:          sharedsaga.StepCreditRecipient,
				accountNumber: payment.ToAccountNumber,
				amount:        payment.InitialAmount,
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber: payment.ToAccountNumber, Amount: payment.InitialAmount.StringFixed(4), UpdateAvailable: true,
							IdempotencyKey: sharedsaga.IdempotencyKey(paySagaID, sharedsaga.StepCreditRecipient),
						})
						return e
					})
				},
			},
		}
		// Commission credit is a saga step so it can be compensated if it fails after
		// debit_sender and credit_recipient have already executed.
		if s.bankRSDAccount != "" && payment.Commission.IsPositive() {
			commAmt := payment.Commission
			bankAcct := s.bankRSDAccount
			steps = append(steps, sagaStep{
				name:          sharedsaga.StepCreditBankCommission,
				accountNumber: bankAcct,
				amount:        commAmt,
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber:   bankAcct,
							Amount:          commAmt.StringFixed(4),
							UpdateAvailable: true,
							IdempotencyKey:  sharedsaga.IdempotencyKey(paySagaID, sharedsaga.StepCreditBankCommission),
						})
						return e
					})
				},
			})
		}
		if err := executeWithSaga(ctx, s.sagaRepo, s.accountClient, s.retryConfig, payment.ID, "payment", steps); err != nil {
			reason := fmt.Sprintf("payment execution failed for %s → %s amount %s %s: %v",
				payment.FromAccountNumber, payment.ToAccountNumber, totalDebit.StringFixed(4), currency, err)
			_ = s.paymentRepo.UpdateStatusWithReason(payment.ID, "failed", reason)
			payment.FailureReason = reason
			s.publishPaymentFailed(ctx, payment, reason)
			s.publishPaymentFailedNotification(ctx, payment)
			return fmt.Errorf("payment %d execution failed: %w", payment.ID, err)
		}
	}

	// Mark payment completed
	now := time.Now()
	payment.CompletedAt = &now
	if err := s.paymentRepo.UpdateStatus(payment.ID, "completed"); err != nil {
		return fmt.Errorf("failed to mark payment %d as completed (from %s to %s): %w",
			payment.ID, payment.FromAccountNumber, payment.ToAccountNumber, err)
	}
	payment.Status = "completed"

	TransactionTotal.WithLabelValues("payment", "completed").Inc()
	TransactionAmountRSDSum.WithLabelValues("payment").Add(payment.FinalAmount.InexactFloat64())
	TransactionProcessingDuration.WithLabelValues("payment").Observe(time.Since(start).Seconds())

	// Publish general notifications for sender and receiver (best-effort, after DB commit)
	s.publishPaymentNotifications(ctx, payment)

	return nil
}

// publishPaymentNotifications emits PAYMENT_SENT/PAYMENT_RECEIVED in-app
// notification intents to the sender and receiver via the notifier (Kafka
// notification.general topic). Uses the Data form — notification-service
// renders the push-channel template registry entry.
//
// Bank-owned sides (owner_id == 0 or 1_000_000_000 sentinel) are skipped.
// All emits are best-effort: lookup or publish failures are silently ignored.
func (s *PaymentService) publishPaymentNotifications(ctx context.Context, payment *model.Payment) {
	if s.notifier == nil || s.accountClient == nil {
		return
	}
	amount := payment.InitialAmount.StringFixed(2)
	fromAcct, err := s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
		AccountNumber: payment.FromAccountNumber,
	})
	if err == nil && fromAcct != nil && fromAcct.GetOwnerId() != 0 && fromAcct.GetOwnerId() != 1_000_000_000 {
		_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  fromAcct.GetOwnerId(),
			Type:    "PAYMENT_SENT",
			Data:    map[string]string{"amount": amount, "to_account": payment.ToAccountNumber},
			RefType: "payment",
			RefID:   payment.ID,
		})
	}
	toAcct, err := s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
		AccountNumber: payment.ToAccountNumber,
	})
	if err == nil && toAcct != nil && toAcct.GetOwnerId() != 0 && toAcct.GetOwnerId() != 1_000_000_000 {
		_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  toAcct.GetOwnerId(),
			Type:    "PAYMENT_RECEIVED",
			Data:    map[string]string{"amount": amount, "from_account": payment.FromAccountNumber},
			RefType: "payment",
			RefID:   payment.ID,
		})
	}
}

// publishPaymentFailedNotification emits a PAYMENT_FAILED in-app notification
// intent to the sender (best-effort). The sender's owner is resolved via the
// accountClient. Skipped if the sender is a bank-owned account (owner_id == 0
// or 1_000_000_000 sentinel).
func (s *PaymentService) publishPaymentFailedNotification(ctx context.Context, payment *model.Payment) {
	if s.notifier == nil || s.accountClient == nil {
		return
	}
	fromAcct, err := s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
		AccountNumber: payment.FromAccountNumber,
	})
	if err != nil || fromAcct == nil {
		return
	}
	if fromAcct.GetOwnerId() == 0 || fromAcct.GetOwnerId() == 1_000_000_000 {
		return
	}
	_ = s.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  fromAcct.GetOwnerId(),
		Type:    "PAYMENT_FAILED",
		Data:    map[string]string{"amount": payment.InitialAmount.StringFixed(2), "failure_reason": payment.FailureReason},
		RefType: "payment",
		RefID:   payment.ID,
	})
}

func (s *PaymentService) GetPayment(id uint64) (*model.Payment, error) {
	return s.paymentRepo.GetByID(id)
}

func (s *PaymentService) ListPaymentsByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error) {
	return s.paymentRepo.ListByAccount(accountNumber, dateFrom, dateTo, statusFilter, amountMin, amountMax, page, pageSize)
}

func (s *PaymentService) ListPaymentsByClient(accountNumbers []string, page, pageSize int) ([]model.Payment, int64, error) {
	return s.paymentRepo.ListByAccountNumbers(accountNumbers, page, pageSize)
}
