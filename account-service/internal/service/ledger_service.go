package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

type LedgerService struct {
	ledgerRepo *repository.LedgerRepository
	db         *gorm.DB
}

func NewLedgerService(ledgerRepo *repository.LedgerRepository, db *gorm.DB) *LedgerService {
	return &LedgerService{ledgerRepo: ledgerRepo, db: db}
}

// Transfer moves amount from fromAccount to toAccount atomically.
// Creates two ledger entries (debit + credit) in a single DB transaction.
func (s *LedgerService) Transfer(fromAccount, toAccount string, amount decimal.Decimal, description, refID, refType string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.ledgerRepo.DebitWithLock(tx, fromAccount, amount, description, refID, refType); err != nil {
			return err
		}
		_, err := s.ledgerRepo.CreditWithLock(tx, toAccount, amount, description, refID, refType)
		return err
	})
}

// Credit adds funds to an account atomically.
func (s *LedgerService) Credit(accountNumber string, amount decimal.Decimal, description, refID, refType string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ledgerRepo.CreditWithLock(tx, accountNumber, amount, description, refID, refType)
		return err
	})
}

// Debit removes funds from an account atomically.
func (s *LedgerService) Debit(accountNumber string, amount decimal.Decimal, description, refID, refType string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ledgerRepo.DebitWithLock(tx, accountNumber, amount, description, refID, refType)
		return err
	})
}

// GetLedgerEntries returns paginated ledger entries for an account.
func (s *LedgerService) GetLedgerEntries(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
	return s.ledgerRepo.GetEntriesByAccount(accountNumber, page, pageSize)
}

// ReconcileBalance verifies that the account's current balance matches the sum of all its ledger entries.
// Credits increase the balance; debits decrease it. Returns nil if consistent, error with details if not.
//
// All three reads run in a REPEATABLE READ transaction so that a concurrent transaction committing
// between them cannot cause a false mismatch report.
func (s *LedgerService) ReconcileBalance(_ context.Context, accountNumber string) error {
	var reconcileErr error
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		// Sum credits.
		var creditTotal string
		if err := tx.Model(&model.LedgerEntry{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("account_number = ? AND entry_type = ?", accountNumber, "credit").
			Scan(&creditTotal).Error; err != nil {
			return fmt.Errorf("reconcile: failed to sum credit entries for account %s: %w", accountNumber, err)
		}
		credits, _ := decimal.NewFromString(creditTotal)

		// Sum debits.
		var debitTotal string
		if err := tx.Model(&model.LedgerEntry{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("account_number = ? AND entry_type = ?", accountNumber, "debit").
			Scan(&debitTotal).Error; err != nil {
			return fmt.Errorf("reconcile: failed to sum debit entries for account %s: %w", accountNumber, err)
		}
		debits, _ := decimal.NewFromString(debitTotal)

		net := credits.Sub(debits)

		// Fetch the account's stored balance — within the same snapshot.
		var acct model.Account
		if err := tx.Where("account_number = ?", accountNumber).First(&acct).Error; err != nil {
			return fmt.Errorf("reconcile: failed to fetch account %s: %w", accountNumber, err)
		}

		if !acct.Balance.Equal(net) {
			reconcileErr = fmt.Errorf("reconcile mismatch for account %s: stored balance=%s, ledger net=%s (credits=%s, debits=%s)",
				accountNumber, acct.Balance.StringFixed(4), net.StringFixed(4),
				credits.StringFixed(4), debits.StringFixed(4))
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})

	if txErr != nil {
		return txErr
	}
	return reconcileErr
}
