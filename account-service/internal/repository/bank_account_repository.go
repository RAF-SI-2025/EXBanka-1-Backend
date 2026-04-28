package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/exbanka/account-service/internal/model"
	shared "github.com/exbanka/contract/shared"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInsufficientBankLiquidity = errors.New("bank insufficient liquidity")
	ErrBankAccountNotFound       = errors.New("bank sentinel account not found for currency")
)

// BankAccountRepository handles atomic operations on bank sentinel accounts.
type BankAccountRepository struct {
	db *gorm.DB
}

// NewBankAccountRepository creates a new BankAccountRepository.
func NewBankAccountRepository(db *gorm.DB) *BankAccountRepository {
	return &BankAccountRepository{db: db}
}

// BankOpResult is returned by DebitBank and CreditBank.
type BankOpResult struct {
	AccountNumber string
	NewBalance    string // 4-decimal fixed-point string, e.g. "950000.0000"
	Replayed      bool   // true if this call was a no-op idempotency replay
}

// DebitBank atomically decreases the bank sentinel account for the given currency.
// Idempotent by reference: a second call with the same (reference, "debit") returns
// the cached result instead of double-applying.
func (r *BankAccountRepository) DebitBank(ctx context.Context, currency string, amount decimal.Decimal, reference, reason string) (*BankOpResult, error) {
	return r.applyBankOp(ctx, currency, amount, reference, reason, "debit")
}

// CreditBank atomically increases the bank sentinel account for the given currency.
// Idempotent by reference.
func (r *BankAccountRepository) CreditBank(ctx context.Context, currency string, amount decimal.Decimal, reference, reason string) (*BankOpResult, error) {
	return r.applyBankOp(ctx, currency, amount, reference, reason, "credit")
}

func (r *BankAccountRepository) applyBankOp(ctx context.Context, currency string, amount decimal.Decimal, reference, reason, direction string) (*BankOpResult, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive, got %s", amount.String())
	}
	if reference == "" {
		return nil, errors.New("reference is required")
	}

	var result *BankOpResult
	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Idempotency check: if we already processed (reference, direction), return cached result.
		var existing model.BankOperation
		lookupErr := tx.Where("reference = ? AND direction = ?", reference, direction).First(&existing).Error
		if lookupErr == nil {
			result = &BankOpResult{
				AccountNumber: existing.AccountNumber,
				NewBalance:    existing.NewBalance.StringFixed(4),
				Replayed:      true,
			}
			return nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("lookup bank_operations: %w", lookupErr)
		}

		// Lock the bank sentinel row for this currency.
		var acct model.Account
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_bank_account = ? AND currency_code = ?", true, currency).
			First(&acct).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return ErrBankAccountNotFound
		}
		if findErr != nil {
			return fmt.Errorf("load bank sentinel: %w", findErr)
		}

		before := acct.Balance
		switch direction {
		case "debit":
			if acct.Balance.LessThan(amount) {
				return ErrInsufficientBankLiquidity
			}
			acct.Balance = acct.Balance.Sub(amount)
			acct.AvailableBalance = acct.AvailableBalance.Sub(amount)
		case "credit":
			acct.Balance = acct.Balance.Add(amount)
			acct.AvailableBalance = acct.AvailableBalance.Add(amount)
		default:
			return fmt.Errorf("unknown direction %q", direction)
		}

		saveRes := tx.Save(&acct)
		if saveRes.Error != nil {
			return fmt.Errorf("save bank sentinel: %w", saveRes.Error)
		}
		if saveRes.RowsAffected == 0 {
			return fmt.Errorf("save bank sentinel: %w", shared.ErrOptimisticLock)
		}

		op := model.BankOperation{
			Reference:     reference,
			Direction:     direction,
			Currency:      currency,
			Amount:        amount,
			AccountNumber: acct.AccountNumber,
			NewBalance:    acct.Balance,
			Reason:        reason,
		}
		if createErr := tx.Create(&op).Error; createErr != nil {
			return fmt.Errorf("record bank_operation: %w", createErr)
		}

		ledger := &model.LedgerEntry{
			AccountNumber: acct.AccountNumber,
			EntryType:     direction,
			Amount:        amount,
			BalanceBefore: before,
			BalanceAfter:  acct.Balance,
			Description:   reason,
			ReferenceID:   reference,
			ReferenceType: "bank_op",
		}
		if lerr := tx.Create(ledger).Error; lerr != nil {
			return fmt.Errorf("write ledger entry: %w", lerr)
		}

		result = &BankOpResult{
			AccountNumber: acct.AccountNumber,
			NewBalance:    acct.Balance.StringFixed(4),
			Replayed:      false,
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return result, nil
}
