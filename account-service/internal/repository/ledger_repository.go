package repository

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/shared/saga"
)

// StampSagaContext copies saga_id and saga_step from ctx onto the ledger
// entry so cross-service audit queries can find every row a saga touched.
// Both fields are nullable; non-saga callers (REST handlers, crons) leave
// them empty. Exported so the reservation/settlement path in the service
// layer can stamp its hand-built ledger entries with the same metadata.
func StampSagaContext(ctx context.Context, entry *model.LedgerEntry) {
	if id, ok := saga.SagaIDFromContext(ctx); ok && id != "" {
		v := id
		entry.SagaID = &v
	}
	if step, ok := saga.SagaStepFromContext(ctx); ok && step != "" {
		v := string(step)
		entry.SagaStep = &v
	}
}

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
// Returns the ledger entry created. When ctx carries a saga_id / saga_step
// (set by the gRPC server saga-context interceptor on incoming saga-callee
// RPCs), both are stamped onto the new ledger row for cross-service audit.
func (r *LedgerRepository) DebitWithLock(ctx context.Context, tx *gorm.DB, accountNumber string, amount decimal.Decimal, description, refID, refType string) (*model.LedgerEntry, error) {
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
	StampSagaContext(ctx, entry)
	return entry, tx.Create(entry).Error
}

// CreditWithLock updates account balance (credit = add) inside a DB transaction with FOR UPDATE lock.
// When ctx carries a saga_id / saga_step, both are stamped onto the new
// ledger row for cross-service audit.
func (r *LedgerRepository) CreditWithLock(ctx context.Context, tx *gorm.DB, accountNumber string, amount decimal.Decimal, description, refID, refType string) (*model.LedgerEntry, error) {
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
	StampSagaContext(ctx, entry)
	return entry, tx.Create(entry).Error
}
