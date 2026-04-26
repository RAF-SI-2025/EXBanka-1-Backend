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
	"github.com/exbanka/transaction-service/internal/repository"
)

// maxSagaCompensationRetries is the number of recovery-loop failures after which
// a compensating step is moved to the dead-letter queue.
const maxSagaCompensationRetries = 10

// sagaPublisher is the subset of *kafka.Producer used by the recovery goroutine.
type sagaPublisher interface {
	PublishSagaDeadLetter(ctx context.Context, msg kafkamsg.SagaDeadLetterMessage) error
}

// TransferRepo abstracts persistence for TransferService (enables unit testing without a real DB).
type TransferRepo interface {
	Create(t *model.Transfer) error
	GetByID(id uint64) (*model.Transfer, error)
	GetByIdempotencyKey(key string) (*model.Transfer, error)
	UpdateStatus(id uint64, status string) error
	UpdateStatusWithReason(id uint64, status, reason string) error
	ListByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error)
}

type TransferService struct {
	transferRepo      TransferRepo
	exchangeClient    ExchangeClientIface
	accountClient     accountpb.AccountServiceClient
	bankAccountClient accountpb.BankAccountServiceClient
	feeSvc            *FeeService
	producer          *kafka.Producer
	retryConfig       shared.RetryConfig
	sagaRepo          *repository.SagaLogRepository // nil-safe: saga logging skipped when nil
	dlPublisher       sagaPublisher                 // nil-safe: dead-letter publishing skipped when nil
}

func NewTransferService(
	transferRepo TransferRepo,
	exchangeClient ExchangeClientIface,
	accountClient accountpb.AccountServiceClient,
	bankAccountClient accountpb.BankAccountServiceClient,
	feeSvc *FeeService,
	producer *kafka.Producer,
	sagaRepo *repository.SagaLogRepository,
) *TransferService {
	return &TransferService{
		transferRepo:      transferRepo,
		exchangeClient:    exchangeClient,
		accountClient:     accountClient,
		bankAccountClient: bankAccountClient,
		feeSvc:            feeSvc,
		producer:          producer,
		retryConfig:       shared.DefaultRetryConfig,
		sagaRepo:          sagaRepo,
		dlPublisher:       producer,
	}
}

// publishTransferFailed publishes a transfer-failed Kafka event (best-effort; errors are only logged).
func (s *TransferService) publishTransferFailed(ctx context.Context, transfer *model.Transfer, reason string) {
	if s.producer == nil {
		return
	}
	msg := kafkamsg.TransferFailedMessage{
		TransferID:        transfer.ID,
		FromAccountNumber: transfer.FromAccountNumber,
		ToAccountNumber:   transfer.ToAccountNumber,
		Amount:            transfer.InitialAmount.StringFixed(4),
		FailureReason:     reason,
	}
	if err := s.producer.PublishTransferFailed(ctx, msg); err != nil {
		log.Printf("TransferService: failed to publish transfer-failed event for transfer %d: %v", transfer.ID, err)
	}
}

// ValidateTransfer checks that a transfer has distinct accounts and a positive amount.
func ValidateTransfer(from, to string, amount decimal.Decimal) error {
	if from == to {
		return fmt.Errorf("from and to accounts must be different, both are %s", from)
	}
	if amount.IsNegative() || amount.IsZero() {
		return fmt.Errorf("transfer amount must be positive, got %s", amount.StringFixed(4))
	}
	return nil
}

func (s *TransferService) CreateTransfer(ctx context.Context, transfer *model.Transfer) error {
	// 1. Idempotency check
	if transfer.IdempotencyKey != "" {
		existing, err := s.transferRepo.GetByIdempotencyKey(transfer.IdempotencyKey)
		if err == nil {
			*transfer = *existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("idempotency check failed for key %q: %w", transfer.IdempotencyKey, err)
		}
	}

	if err := ValidateTransfer(transfer.FromAccountNumber, transfer.ToAccountNumber, transfer.InitialAmount); err != nil {
		return err
	}

	// Validate that both accounts belong to the same client (transfers are intra-client only)
	if s.accountClient != nil {
		var fromAccount, toAccount *accountpb.AccountResponse
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			var e error
			fromAccount, e = s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
				AccountNumber: transfer.FromAccountNumber,
			})
			return e
		}); err != nil {
			return fmt.Errorf("failed to fetch sender account %s: %w", transfer.FromAccountNumber, err)
		}
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			var e error
			toAccount, e = s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
				AccountNumber: transfer.ToAccountNumber,
			})
			return e
		}); err != nil {
			return fmt.Errorf("failed to fetch recipient account %s: %w", transfer.ToAccountNumber, err)
		}
		if fromAccount.OwnerId != toAccount.OwnerId {
			return fmt.Errorf("transfers must be between accounts of the same client; use payments for different-client transactions")
		}

		// Spending limit pre-check
		totalDebit := transfer.InitialAmount.Add(transfer.Commission)
		dailyLimit, _ := decimal.NewFromString(fromAccount.GetDailyLimit())
		monthlyLimit, _ := decimal.NewFromString(fromAccount.GetMonthlyLimit())
		dailySpending, _ := decimal.NewFromString(fromAccount.GetDailySpending())
		monthlySpending, _ := decimal.NewFromString(fromAccount.GetMonthlySpending())

		if !dailyLimit.IsZero() && dailySpending.Add(totalDebit).GreaterThan(dailyLimit) {
			return fmt.Errorf("limit_exceeded: daily spending limit would be exceeded on account %s: current daily spending %s, attempted %s, daily limit %s",
				transfer.FromAccountNumber, dailySpending.StringFixed(4), totalDebit.StringFixed(4), dailyLimit.StringFixed(4))
		}
		if !monthlyLimit.IsZero() && monthlySpending.Add(totalDebit).GreaterThan(monthlyLimit) {
			return fmt.Errorf("limit_exceeded: monthly spending limit would be exceeded on account %s: current monthly spending %s, attempted %s, monthly limit %s",
				transfer.FromAccountNumber, monthlySpending.StringFixed(4), totalDebit.StringFixed(4), monthlyLimit.StringFixed(4))
		}
	}

	transfer.Timestamp = time.Now()

	// Same-currency, same-client = "Prenos" (zero fee, no exchange)
	if transfer.FromCurrency == transfer.ToCurrency || (transfer.FromCurrency == "" && transfer.ToCurrency == "") {
		transfer.ExchangeRate = decimal.NewFromInt(1)
		transfer.FinalAmount = transfer.InitialAmount
		transfer.Commission = decimal.Zero
	} else {
		// Cross-currency transfer: calculate fees and exchange rate
		fromCurrency := transfer.FromCurrency
		if fromCurrency == "" {
			fromCurrency = "RSD"
		}
		commission, err := s.feeSvc.CalculateFee(transfer.InitialAmount, "transfer", fromCurrency)
		if err != nil {
			return fmt.Errorf("fee calculation failed for transfer of %s %s from account %s to %s: %w",
				transfer.InitialAmount.StringFixed(4), fromCurrency, transfer.FromAccountNumber, transfer.ToAccountNumber, err)
		}
		transfer.Commission = commission

		// Determine exchange rate for cross-currency transfers (via RSD intermediate)
		if s.exchangeClient == nil {
			return fmt.Errorf("cross-currency transfers require exchange service")
		}
		convertedAmount, effectiveRate, err := s.exchangeClient.ConvertViaRSD(ctx, transfer.FromCurrency, transfer.ToCurrency, transfer.InitialAmount)
		if err != nil {
			return fmt.Errorf("currency conversion failed: %w", err)
		}
		transfer.FinalAmount = convertedAmount
		transfer.ExchangeRate = effectiveRate
	}

	// Save transfer in pending_verification status (no balance changes yet)
	transfer.Status = "pending_verification"
	if err := s.transferRepo.Create(transfer); err != nil {
		return err
	}

	TransactionTotal.WithLabelValues("transfer", "created").Inc()
	return nil
}

// ExecuteTransfer performs the actual balance changes for a transfer that has been verified.
// The transfer must be in "pending_verification" status.
func (s *TransferService) ExecuteTransfer(ctx context.Context, transferID uint64) error {
	start := time.Now()

	transfer, err := s.transferRepo.GetByID(transferID)
	if err != nil {
		return fmt.Errorf("transfer not found: %w", err)
	}

	// Idempotency: if already completed, nothing to do
	if transfer.Status == "completed" {
		return nil
	}

	if transfer.Status != "pending_verification" {
		return fmt.Errorf("transfer %d is in status %q, expected pending_verification", transferID, transfer.Status)
	}

	// Mark as processing
	if err := s.transferRepo.UpdateStatus(transfer.ID, "processing"); err != nil {
		return fmt.Errorf("failed to mark transfer %d as processing: %w", transfer.ID, err)
	}
	transfer.Status = "processing"

	convertedAmount := transfer.FinalAmount
	totalDebit := transfer.InitialAmount.Add(transfer.Commission)

	// Re-check spending limits at execution time
	if s.accountClient != nil {
		var fromAccount *accountpb.AccountResponse
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			var e error
			fromAccount, e = s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
				AccountNumber: transfer.FromAccountNumber,
			})
			return e
		}); err == nil && fromAccount != nil {
			dailyLimit, _ := decimal.NewFromString(fromAccount.GetDailyLimit())
			monthlyLimit, _ := decimal.NewFromString(fromAccount.GetMonthlyLimit())
			dailySpending, _ := decimal.NewFromString(fromAccount.GetDailySpending())
			monthlySpending, _ := decimal.NewFromString(fromAccount.GetMonthlySpending())

			if !dailyLimit.IsZero() && dailySpending.Add(totalDebit).GreaterThan(dailyLimit) {
				reason := fmt.Sprintf("limit_exceeded: daily spending limit would be exceeded on account %s: current daily spending %s, attempted %s, daily limit %s",
					transfer.FromAccountNumber, dailySpending.StringFixed(4), totalDebit.StringFixed(4), dailyLimit.StringFixed(4))
				_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
				s.publishTransferFailed(ctx, transfer, reason)
				return errors.New(reason)
			}
			if !monthlyLimit.IsZero() && monthlySpending.Add(totalDebit).GreaterThan(monthlyLimit) {
				reason := fmt.Sprintf("limit_exceeded: monthly spending limit would be exceeded on account %s: current monthly spending %s, attempted %s, monthly limit %s",
					transfer.FromAccountNumber, monthlySpending.StringFixed(4), totalDebit.StringFixed(4), monthlyLimit.StringFixed(4))
				_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
				s.publishTransferFailed(ctx, transfer, reason)
				return errors.New(reason)
			}
		}
	}

	isCrossCurrency := transfer.FromCurrency != transfer.ToCurrency &&
		transfer.FromCurrency != "" && transfer.ToCurrency != ""

	if isCrossCurrency {
		// Cross-currency: route through bank accounts.
		// Step 1: debit user FROM account (InitialAmount + Commission)
		// Step 2: credit bank FROM-currency account (same amount — commission absorbed here)
		// Step 3: debit bank TO-currency account (FinalAmount, converted)
		// Step 4: credit user TO account (FinalAmount)
		// Each step failure triggers saga-logged compensation of all prior steps.

		if s.accountClient == nil || s.bankAccountClient == nil {
			reason := "cross-currency transfer requires both accountClient and bankAccountClient"
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return errors.New(reason)
		}

		var bankAccountsResp *accountpb.ListBankAccountsResponse
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			var e error
			bankAccountsResp, e = s.bankAccountClient.ListBankAccounts(ctx, &accountpb.ListBankAccountsRequest{})
			return e
		}); err != nil {
			reason := fmt.Sprintf("failed to fetch bank accounts: %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return errors.New(reason)
		}

		bankFromAccount, err := findBankAccountByCurrency(bankAccountsResp.GetAccounts(), transfer.FromCurrency)
		if err != nil {
			reason := fmt.Sprintf("no bank account for from-currency %s: %v", transfer.FromCurrency, err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return errors.New(reason)
		}

		// Cross-currency saga step 3 debits the bank's to-currency account.
		// findBankAccountByCurrencyWithBalance picks one with sufficient
		// available balance so we don't pick a 0-balance account (the
		// gateway POST /bank-accounts path creates accounts with no
		// initial balance — see findBankAccountByCurrencyWithBalance for
		// the full rationale).
		bankToAccount, err := findBankAccountByCurrencyWithBalance(bankAccountsResp.GetAccounts(), transfer.ToCurrency, convertedAmount)
		if err != nil {
			reason := fmt.Sprintf("no bank account with sufficient balance for to-currency %s: %v", transfer.ToCurrency, err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return errors.New(reason)
		}

		steps := []sagaStep{
			{
				name:          "debit_user_from",
				accountNumber: transfer.FromAccountNumber,
				amount:        totalDebit.Neg(),
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber: transfer.FromAccountNumber, Amount: totalDebit.Neg().StringFixed(4), UpdateAvailable: true,
						})
						return e
					})
				},
			},
			{
				name:          "credit_bank_from",
				accountNumber: bankFromAccount,
				amount:        totalDebit,
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber: bankFromAccount, Amount: totalDebit.StringFixed(4), UpdateAvailable: true,
						})
						return e
					})
				},
			},
			{
				name:          "debit_bank_to",
				accountNumber: bankToAccount,
				amount:        convertedAmount.Neg(),
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber: bankToAccount, Amount: convertedAmount.Neg().StringFixed(4), UpdateAvailable: true,
						})
						return e
					})
				},
			},
			{
				name:          "credit_user_to",
				accountNumber: transfer.ToAccountNumber,
				amount:        convertedAmount,
				execute: func(ctx context.Context) error {
					return shared.Retry(ctx, s.retryConfig, func() error {
						_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
							AccountNumber: transfer.ToAccountNumber, Amount: convertedAmount.StringFixed(4), UpdateAvailable: true,
						})
						return e
					})
				},
			},
		}
		if err := executeWithSaga(ctx, s.sagaRepo, s.accountClient, s.retryConfig, transfer.ID, "transfer", steps); err != nil {
			reason := fmt.Sprintf("cross-currency transfer execution failed: %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return err
		}

	} else {
		// Same-currency: direct debit/credit, no bank intermediate.
		if s.accountClient != nil {
			steps := []sagaStep{
				{
					name:          "debit_sender",
					accountNumber: transfer.FromAccountNumber,
					amount:        totalDebit.Neg(),
					execute: func(ctx context.Context) error {
						return shared.Retry(ctx, s.retryConfig, func() error {
							_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
								AccountNumber: transfer.FromAccountNumber, Amount: totalDebit.Neg().StringFixed(4), UpdateAvailable: true,
							})
							return e
						})
					},
				},
				{
					name:          "credit_recipient",
					accountNumber: transfer.ToAccountNumber,
					amount:        convertedAmount,
					execute: func(ctx context.Context) error {
						return shared.Retry(ctx, s.retryConfig, func() error {
							_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
								AccountNumber: transfer.ToAccountNumber, Amount: convertedAmount.StringFixed(4), UpdateAvailable: true,
							})
							return e
						})
					},
				},
			}
			if err := executeWithSaga(ctx, s.sagaRepo, s.accountClient, s.retryConfig, transfer.ID, "transfer", steps); err != nil {
				reason := fmt.Sprintf("same-currency transfer execution failed: %v", err)
				_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
				s.publishTransferFailed(ctx, transfer, reason)
				return err
			}
		}
	}

	// Mark transfer completed
	now := time.Now()
	transfer.CompletedAt = &now
	if err := s.transferRepo.UpdateStatus(transfer.ID, "completed"); err != nil {
		return err
	}
	transfer.Status = "completed"

	TransactionTotal.WithLabelValues("transfer", "completed").Inc()
	TransactionAmountRSDSum.WithLabelValues("transfer").Add(transfer.FinalAmount.InexactFloat64())
	TransactionProcessingDuration.WithLabelValues("transfer").Observe(time.Since(start).Seconds())

	// Publish general notification for the account owner (best-effort, after DB commit)
	s.publishTransferNotification(ctx, transfer)

	return nil
}

// publishTransferNotification sends a money_sent general notification for the transfer owner.
// Transfers are intra-client (same owner), so only one notification is needed.
func (s *TransferService) publishTransferNotification(ctx context.Context, transfer *model.Transfer) {
	if s.producer == nil || s.accountClient == nil {
		return
	}
	fromAcct, err := s.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{
		AccountNumber: transfer.FromAccountNumber,
	})
	if err == nil && fromAcct != nil {
		_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  fromAcct.GetOwnerId(),
			Type:    "money_sent",
			Title:   "Transfer Completed",
			Message: fmt.Sprintf("Transfer of %s from %s to %s completed", transfer.InitialAmount.StringFixed(2), transfer.FromAccountNumber, transfer.ToAccountNumber),
			RefType: "transfer",
			RefID:   transfer.ID,
		})
	}
}

func (s *TransferService) GetTransfer(id uint64) (*model.Transfer, error) {
	return s.transferRepo.GetByID(id)
}

func (s *TransferService) ListTransfersByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error) {
	return s.transferRepo.ListByAccountNumbers(accountNumbers, page, pageSize)
}

// runRecoveryTick processes one pass of the compensation recovery loop.
// For each compensating saga step it attempts UpdateBalance; on success it marks the
// step completed, on failure it increments RetryCount and — when the count reaches
// maxSagaCompensationRetries — publishes a dead-letter Kafka event and moves the step
// to "dead_letter" so it is no longer retried.
func (s *TransferService) runRecoveryTick(ctx context.Context) {
	if s.sagaRepo == nil || s.accountClient == nil {
		return
	}
	pending, err := s.sagaRepo.FindPendingCompensations()
	if err != nil {
		log.Printf("saga recovery: failed to fetch pending compensations: %v", err)
		return
	}
	for _, comp := range pending {
		comp := comp
		retryErr := shared.Retry(ctx, s.retryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   comp.AccountNumber,
				Amount:          comp.Amount.StringFixed(4),
				UpdateAvailable: true,
			})
			return e
		})
		if retryErr != nil {
			if incErr := s.sagaRepo.IncrementRetryCount(comp.ID); incErr != nil {
				log.Printf("saga recovery: failed to increment retry count for comp %d: %v — skipping dead-letter check", comp.ID, incErr)
				continue
			}
			// comp.RetryCount was loaded at the start of this tick and is stale
			// after IncrementRetryCount. Because StartCompensationRecovery runs
			// as a single goroutine (one tick at a time), no other writer can
			// change retry_count concurrently — so comp.RetryCount+1 is always
			// the correct post-increment value.
			updatedCount := comp.RetryCount + 1
			if updatedCount >= maxSagaCompensationRetries {
				dlMsg := kafkamsg.SagaDeadLetterMessage{
					SagaLogID:       comp.ID,
					SagaID:          comp.SagaID,
					TransactionID:   comp.TransactionID,
					TransactionType: comp.TransactionType,
					StepName:        comp.StepName,
					AccountNumber:   comp.AccountNumber,
					Amount:          comp.Amount.StringFixed(4),
					RetryCount:      updatedCount,
					LastError:       retryErr.Error(),
				}
				// Mark dead_letter in DB first; if this fails, skip publishing to avoid
				// duplicate Kafka events on the next tick (step would still be compensating).
				if dlErr := s.sagaRepo.MarkDeadLetter(comp.ID, retryErr.Error()); dlErr != nil {
					log.Printf("saga recovery: failed to mark comp %d as dead_letter: %v — will retry next tick", comp.ID, dlErr)
					continue
				}
				if s.dlPublisher != nil {
					if pubErr := s.dlPublisher.PublishSagaDeadLetter(ctx, dlMsg); pubErr != nil {
						log.Printf("saga recovery: failed to publish dead-letter for comp %d: %v", comp.ID, pubErr)
					}
				}
				log.Printf("saga recovery: compensation %d moved to dead-letter after %d retries (account %s amount %s)",
					comp.ID, updatedCount, comp.AccountNumber, comp.Amount.StringFixed(4))
			} else {
				log.Printf("saga recovery: compensation %d still failing (retry %d/%d) for account %s: %v",
					comp.ID, updatedCount, maxSagaCompensationRetries, comp.AccountNumber, retryErr)
			}
		} else {
			_ = s.sagaRepo.CompleteStep(comp.ID)
			log.Printf("saga recovery: compensation %d succeeded for account %s amount %s",
				comp.ID, comp.AccountNumber, comp.Amount.StringFixed(4))
		}
	}
}

// StartCompensationRecovery starts a background goroutine that periodically retries
// all saga log entries in "compensating" status. These are compensation steps that
// previously failed to execute; the recovery loop retries them until they succeed or
// reach maxSagaCompensationRetries failures, at which point they are moved to dead_letter.
// Both transfer and payment compensations are handled here because both only require
// an accountClient.UpdateBalance call.
func (s *TransferService) StartCompensationRecovery(ctx context.Context) {
	// Run immediate recovery pass at startup
	if s.sagaRepo != nil {
		pending, err := s.sagaRepo.FindPendingCompensations()
		if err != nil {
			log.Printf("saga recovery: failed to check pending compensations at startup: %v", err)
		} else {
			log.Printf("saga recovery: startup check found %d pending compensation(s)", len(pending))
		}
	}
	s.runRecoveryTick(ctx)

	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runRecoveryTick(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// findBankAccountByCurrency returns the account number of the first bank account
// matching the given currency from the provided list.
func findBankAccountByCurrency(accounts []*accountpb.AccountResponse, currency string) (string, error) {
	for _, a := range accounts {
		if a.GetCurrencyCode() == currency {
			return a.GetAccountNumber(), nil
		}
	}
	return "", fmt.Errorf("no bank account found for currency %s", currency)
}

// findBankAccountByCurrencyWithBalance returns the first bank account in the
// list whose currency matches AND whose available balance covers `need`.
// Falls back to the first matching account if none have enough — preserves
// the legacy "always return something" behavior so the caller still gets a
// useful error from the saga's actual debit step rather than a confusing
// pre-check failure.
//
// Why this exists: the gateway's POST /bank-accounts path (admin/test
// helper) creates bank accounts with no initial balance — the gRPC
// CreateBankAccountRequest proto has no initial_balance field. Tests
// using that path leave 0-balance bank accounts in the DB. Without the
// balance filter, findBankAccountByCurrency would return the first match
// from ListBankAccounts (insertion order), which can be a 0-balance
// account, causing every cross-currency transfer to fail with a
// confusing "insufficient funds on account X" error from a downstream
// FOR UPDATE check.
func findBankAccountByCurrencyWithBalance(accounts []*accountpb.AccountResponse, currency string, need decimal.Decimal) (string, error) {
	var firstMatch string
	for _, a := range accounts {
		if a.GetCurrencyCode() != currency {
			continue
		}
		if firstMatch == "" {
			firstMatch = a.GetAccountNumber()
		}
		if avail, err := decimal.NewFromString(a.GetAvailableBalance()); err == nil && avail.GreaterThanOrEqual(need) {
			return a.GetAccountNumber(), nil
		}
	}
	if firstMatch != "" {
		return firstMatch, nil
	}
	return "", fmt.Errorf("no bank account found for currency %s", currency)
}
