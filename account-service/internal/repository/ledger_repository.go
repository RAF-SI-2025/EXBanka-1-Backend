package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
)

type LedgerRepository struct {
	db *gorm.DB
}

func NewLedgerRepository(db *gorm.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// RecordEntry saves a ledger entry.
func (r *LedgerRepository) RecordEntry(entry *model.LedgerEntry) error {
	return r.db.Create(entry).Error
}

// GetEntriesByAccount returns paginated ledger entries for an account.
func (r *LedgerRepository) GetEntriesByAccount(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
	var entries []model.LedgerEntry
	var total int64
	offset := (page - 1) * pageSize
	if err := r.db.Model(&model.LedgerEntry{}).Where("account_number = ?", accountNumber).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := r.db.Where("account_number = ?", accountNumber).
		Order("created_at DESC").
		Limit(pageSize).Offset(offset).
		Find(&entries).Error
	return entries, total, err
}

// DebitWithLock updates account balance (debit = subtract) inside a DB transaction with FOR UPDATE lock.
// Returns the ledger entry created.
func (r *LedgerRepository) DebitWithLock(tx *gorm.DB, accountNumber string, amount decimal.Decimal, description, refID, refType string) (*model.LedgerEntry, error) {
	var acct model.Account
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("account_number = ?", accountNumber).
		First(&acct).Error; err != nil {
		return nil, err
	}
	if acct.AvailableBalance.LessThan(amount) {
		return nil, errors.New("insufficient funds")
	}
	before := acct.Balance
	acct.Balance = acct.Balance.Sub(amount)
	acct.AvailableBalance = acct.AvailableBalance.Sub(amount)
	if !acct.IsBankAccount {
		acct.DailySpending = acct.DailySpending.Add(amount)
		acct.MonthlySpending = acct.MonthlySpending.Add(amount)
	}
	if err := tx.Save(&acct).Error; err != nil {
		return nil, err
	}
	entry := &model.LedgerEntry{
		AccountNumber: accountNumber,
		EntryType:     "debit",
		Amount:        amount,
		BalanceBefore: before,
		BalanceAfter:  acct.Balance,
		Description:   description,
		ReferenceID:   refID,
		ReferenceType: refType,
	}
	return entry, tx.Create(entry).Error
}

// CreditWithLock updates account balance (credit = add) inside a DB transaction with FOR UPDATE lock.
func (r *LedgerRepository) CreditWithLock(tx *gorm.DB, accountNumber string, amount decimal.Decimal, description, refID, refType string) (*model.LedgerEntry, error) {
	var acct model.Account
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("account_number = ?", accountNumber).
		First(&acct).Error; err != nil {
		return nil, err
	}
	before := acct.Balance
	acct.Balance = acct.Balance.Add(amount)
	acct.AvailableBalance = acct.AvailableBalance.Add(amount)
	if err := tx.Save(&acct).Error; err != nil {
		return nil, err
	}
	entry := &model.LedgerEntry{
		AccountNumber: accountNumber,
		EntryType:     "credit",
		Amount:        amount,
		BalanceBefore: before,
		BalanceAfter:  acct.Balance,
		Description:   description,
		ReferenceID:   refID,
		ReferenceType: refType,
	}
	return entry, tx.Create(entry).Error
}
