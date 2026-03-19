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

type TransferService struct {
	transferRepo   *repository.TransferRepository
	exchangeSvc    *ExchangeService
	accountClient  accountpb.AccountServiceClient
	feeSvc         *FeeService
	bankRSDAccount string // account number of bank's RSD account
}

func NewTransferService(transferRepo *repository.TransferRepository, exchangeSvc *ExchangeService, accountClient accountpb.AccountServiceClient, feeSvc *FeeService, bankRSDAccount string) *TransferService {
	return &TransferService{
		transferRepo:   transferRepo,
		exchangeSvc:    exchangeSvc,
		accountClient:  accountClient,
		feeSvc:         feeSvc,
		bankRSDAccount: bankRSDAccount,
	}
}

// ValidateTransfer checks that a transfer has distinct accounts and a positive amount.
func ValidateTransfer(from, to string, amount decimal.Decimal) error {
	if from == to {
		return errors.New("from and to accounts must be different")
	}
	if amount.IsNegative() || amount.IsZero() {
		return errors.New("amount must be positive")
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
			return fmt.Errorf("idempotency check failed: %w", err)
		}
	}

	if err := ValidateTransfer(transfer.FromAccountNumber, transfer.ToAccountNumber, transfer.InitialAmount); err != nil {
		return err
	}

	fromCurrency := transfer.FromCurrency
	if fromCurrency == "" {
		fromCurrency = "RSD"
	}
	commission, err := s.feeSvc.CalculateFee(transfer.InitialAmount, "transfer", fromCurrency)
	if err != nil {
		return fmt.Errorf("fee calculation failed: %w", err)
	}
	transfer.Commission = commission
	transfer.Timestamp = time.Now()

	// 2. Determine exchange rate for cross-currency transfers
	exchangeRate := decimal.NewFromInt(1)
	if transfer.FromCurrency != "" && transfer.ToCurrency != "" && transfer.FromCurrency != transfer.ToCurrency && s.exchangeSvc != nil {
		rate, err := s.exchangeSvc.GetExchangeRate(transfer.FromCurrency, transfer.ToCurrency)
		if err == nil && rate != nil {
			exchangeRate = rate.SellRate
		}
	}
	transfer.ExchangeRate = exchangeRate
	convertedAmount := ConvertAmount(transfer.InitialAmount, exchangeRate)
	transfer.FinalAmount = convertedAmount

	transfer.Status = "processing"
	if err := s.transferRepo.Create(transfer); err != nil {
		return err
	}

	totalDebit := transfer.InitialAmount.Add(transfer.Commission)

	// 3. Debit sender account (in from-currency)
	if s.accountClient != nil {
		debitAmt := totalDebit.Neg().StringFixed(4)
		if err := shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   transfer.FromAccountNumber,
				Amount:          debitAmt,
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			reason := fmt.Sprintf("debit failed: %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			transfer.Status = "failed"
			transfer.FailureReason = reason
			return fmt.Errorf("failed to debit sender account: %w", err)
		}
	}

	// 4. Credit recipient account (in to-currency, converted amount)
	if s.accountClient != nil {
		creditAmt := convertedAmount.StringFixed(4)
		if err := shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   transfer.ToAccountNumber,
				Amount:          creditAmt,
				UpdateAvailable: true,
			})
			return e
		}); err != nil {
			// Compensating transaction: reverse the debit
			reverseAmt := totalDebit.StringFixed(4)
			_ = shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
				_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:   transfer.FromAccountNumber,
					Amount:          reverseAmt,
					UpdateAvailable: true,
				})
				return e
			})
			reason := fmt.Sprintf("credit failed (debit reversed): %v", err)
			_ = s.transferRepo.UpdateStatusWithReason(transfer.ID, "failed", reason)
			transfer.Status = "failed"
			transfer.FailureReason = reason
			return fmt.Errorf("failed to credit recipient account: %w", err)
		}
	}

	// 5. Credit commission to bank's own RSD account (best-effort)
	if s.bankRSDAccount != "" && transfer.Commission.IsPositive() {
		commissionAmt := transfer.Commission.StringFixed(4)
		_ = shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
			_, e := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   s.bankRSDAccount,
				Amount:          commissionAmt,
				UpdateAvailable: true,
			})
			return e
		})
	}

	// 6. Mark transfer completed
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

func (s *TransferService) ListTransfersByClient(clientID uint64, page, pageSize int) ([]model.Transfer, int64, error) {
	return s.transferRepo.ListByClient(clientID, page, pageSize)
}
