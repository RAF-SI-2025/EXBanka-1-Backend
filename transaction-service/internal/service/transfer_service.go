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
}

func NewTransferService(
	transferRepo TransferRepo,
	exchangeClient ExchangeClientIface,
	accountClient accountpb.AccountServiceClient,
	bankAccountClient accountpb.BankAccountServiceClient,
	feeSvc *FeeService,
	producer *kafka.Producer,
) *TransferService {
	return &TransferService{
		transferRepo:      transferRepo,
		exchangeClient:    exchangeClient,
		accountClient:     accountClient,
		bankAccountClient: bankAccountClient,
		feeSvc:            feeSvc,
		producer:          producer,
		retryConfig:       shared.DefaultRetryConfig,
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

	return nil
}

// ExecuteTransfer performs the actual balance changes for a transfer that has been verified.
// The transfer must be in "pending_verification" status.
func (s *TransferService) ExecuteTransfer(ctx context.Context, transferID uint64) error {
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
		// Each step failure reverses all prior steps.

		if s.accountClient == nil || s.bankAccountClient == nil {
			reason := "cross-currency transfer requires both accountClient and bankAccountClient"
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("%s", reason)
		}

		bankAccountsResp, err := s.bankAccountClient.ListBankAccounts(ctx, &accountpb.ListBankAccountsRequest{})
		if err != nil {
			reason := fmt.Sprintf("failed to fetch bank accounts: %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("%s", reason)
		}

		bankFromAccount, err := findBankAccountByCurrency(bankAccountsResp.GetAccounts(), transfer.FromCurrency)
		if err != nil {
			reason := fmt.Sprintf("no bank account for from-currency %s: %v", transfer.FromCurrency, err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("%s", reason)
		}

		bankToAccount, err := findBankAccountByCurrency(bankAccountsResp.GetAccounts(), transfer.ToCurrency)
		if err != nil {
			reason := fmt.Sprintf("no bank account for to-currency %s: %v", transfer.ToCurrency, err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("%s", reason)
		}

		// Step 1: Debit user FROM account.
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   transfer.FromAccountNumber,
				Amount:          totalDebit.Neg().StringFixed(4),
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			reason := fmt.Sprintf("step1 debit user failed: %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("cross-currency transfer failed at step 1: %w", err)
		}

		// Step 2: Credit bank FROM-currency account.
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   bankFromAccount,
				Amount:          totalDebit.StringFixed(4),
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			// Reverse step 1.
			_ = shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   transfer.FromAccountNumber,
					Amount:          totalDebit.StringFixed(4),
					UpdateAvailable: true,
				})
				return e
			})
			reason := fmt.Sprintf("step2 credit bank FROM failed (step1 reversed): %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("cross-currency transfer failed at step 2: %w", err)
		}

		// Step 3: Debit bank TO-currency account.
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   bankToAccount,
				Amount:          convertedAmount.Neg().StringFixed(4),
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			// Reverse steps 2 and 1.
			_ = shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   bankFromAccount,
					Amount:          totalDebit.Neg().StringFixed(4),
					UpdateAvailable: true,
				})
				return e
			})
			_ = shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   transfer.FromAccountNumber,
					Amount:          totalDebit.StringFixed(4),
					UpdateAvailable: true,
				})
				return e
			})
			reason := fmt.Sprintf("step3 debit bank TO failed (steps 1-2 reversed): %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("cross-currency transfer failed at step 3: %w", err)
		}

		// Step 4: Credit user TO account.
		if err := shared.Retry(ctx, s.retryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   transfer.ToAccountNumber,
				Amount:          convertedAmount.StringFixed(4),
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			// Reverse steps 3, 2, and 1.
			_ = shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   bankToAccount,
					Amount:          convertedAmount.StringFixed(4),
					UpdateAvailable: true,
				})
				return e
			})
			_ = shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   bankFromAccount,
					Amount:          totalDebit.Neg().StringFixed(4),
					UpdateAvailable: true,
				})
				return e
			})
			_ = shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   transfer.FromAccountNumber,
					Amount:          totalDebit.StringFixed(4),
					UpdateAvailable: true,
				})
				return e
			})
			reason := fmt.Sprintf("step4 credit user TO failed (all steps reversed): %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			s.publishTransferFailed(ctx, transfer, reason)
			return fmt.Errorf("cross-currency transfer failed at step 4: %w", err)
		}

	} else {
		// Same-currency: direct debit/credit, no bank intermediate.
		if s.accountClient != nil {
			debitAmt := totalDebit.Neg().StringFixed(4)
			if err := shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   transfer.FromAccountNumber,
					Amount:          debitAmt,
					UpdateAvailable: true,
				})
				return e
			}); err != nil {
				reason := fmt.Sprintf("debit failed: %v", err)
				_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
				s.publishTransferFailed(ctx, transfer, reason)
				return fmt.Errorf("failed to debit sender account: %w", err)
			}

			creditAmt := convertedAmount.StringFixed(4)
			if err := shared.Retry(ctx, s.retryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   transfer.ToAccountNumber,
					Amount:          creditAmt,
					UpdateAvailable: true,
				})
				return e
			}); err != nil {
				// Compensating: reverse the debit.
				_ = shared.Retry(ctx, s.retryConfig, func() error {
					_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
						AccountNumber:   transfer.FromAccountNumber,
						Amount:          totalDebit.StringFixed(4),
						UpdateAvailable: true,
					})
					return e
				})
				reason := fmt.Sprintf("credit failed (debit reversed): %v", err)
				_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
				s.publishTransferFailed(ctx, transfer, reason)
				return fmt.Errorf("failed to credit recipient account: %w", err)
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
	return nil
}

func (s *TransferService) GetTransfer(id uint64) (*model.Transfer, error) {
	return s.transferRepo.GetByID(id)
}

func (s *TransferService) ListTransfersByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error) {
	return s.transferRepo.ListByAccountNumbers(accountNumbers, page, pageSize)
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
