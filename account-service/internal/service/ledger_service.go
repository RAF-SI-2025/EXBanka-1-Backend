package service

import (
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
