package repository

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AccountRepository struct {
	db *gorm.DB
}

func NewAccountRepository(db *gorm.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) Create(account *model.Account) error {
	return r.db.Create(account).Error
}

func (r *AccountRepository) GetByID(id uint64) (*model.Account, error) {
	var account model.Account
	if err := r.db.First(&account, id).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *AccountRepository) GetByNumber(accountNumber string) (*model.Account, error) {
	var account model.Account
	if err := r.db.Where("account_number = ?", accountNumber).First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *AccountRepository) ListByClient(clientID uint64, page, pageSize int) ([]model.Account, int64, error) {
	var accounts []model.Account
	var total int64

	query := r.db.Model(&model.Account{}).Where("owner_id = ?", clientID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Find(&accounts).Error; err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

func (r *AccountRepository) ListAll(nameFilter, numberFilter, typeFilter string, page, pageSize int) ([]model.Account, int64, error) {
	var accounts []model.Account
	var total int64

	query := r.db.Model(&model.Account{})
	if nameFilter != "" {
		query = query.Where("account_name ILIKE ?", "%"+nameFilter+"%")
	}
	if numberFilter != "" {
		query = query.Where("account_number ILIKE ?", "%"+numberFilter+"%")
	}
	if typeFilter != "" {
		query = query.Where("account_type = ?", typeFilter)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Find(&accounts).Error; err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

func (r *AccountRepository) ExistsByNameAndOwner(name string, ownerID uint64, excludeID uint64) (bool, error) {
	var count int64
	query := r.db.Model(&model.Account{}).Where("account_name = ? AND owner_id = ?", name, ownerID)
	if excludeID > 0 {
		query = query.Where("id != ?", excludeID)
	}
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *AccountRepository) UpdateName(id, clientID uint64, newName string) error {
	result := r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ? AND owner_id = ?", id, clientID).Update("account_name", newName)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) UpdateLimits(id uint64, updates map[string]interface{}) error {
	result := r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) UpdateStatus(id uint64, status string) error {
	result := r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("id = ?", id).Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) ListBankAccounts() ([]model.Account, error) {
	var accounts []model.Account
	err := r.db.Where("is_bank_account = ?", true).Find(&accounts).Error
	return accounts, err
}

func (r *AccountRepository) ListBankAccountsByCurrency(currency string) ([]model.Account, error) {
	var accounts []model.Account
	err := r.db.Where("is_bank_account = ? AND currency_code = ? AND status = ?", true, currency, "active").Find(&accounts).Error
	return accounts, err
}

func (r *AccountRepository) SoftDelete(id uint64) error {
	result := r.db.Delete(&model.Account{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) ResetDailySpending() error {
	// SkipHooks: bulk reset intentionally bypasses per-row version checks.
	return r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("status = ?", "active").
		Update("daily_spending", 0).Error
}

func (r *AccountRepository) ResetMonthlySpending() error {
	// SkipHooks: bulk reset intentionally bypasses per-row version checks.
	return r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).Where("status = ?", "active").
		Update("monthly_spending", 0).Error
}

func (r *AccountRepository) ListActiveAccountsWithMaintenanceFee() ([]model.Account, error) {
	var accounts []model.Account
	err := r.db.Where("status = ? AND maintenance_fee > 0 AND is_bank_account = ?", "active", false).Find(&accounts).Error
	return accounts, err
}

// UpdateBalance atomically locks the account row with SELECT FOR UPDATE, enforces
// spending limits for debits on client accounts, checks sufficient funds, then
// updates balance (and optionally available_balance) and spending counters in a
// single transaction. This eliminates the TOCTOU race between separate
// GetByNumber → UpdateBalance → UpdateSpending calls.
func (r *AccountRepository) UpdateBalance(accountNumber string, amount decimal.Decimal, updateAvailable bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Lock the row for the duration of this transaction.
		var acct model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_number = ?", accountNumber).
			First(&acct).Error; err != nil {
			return err
		}

		isDebit := amount.IsNegative()

		// For debits on client accounts, enforce spending limits inside the lock.
		if isDebit && !acct.IsBankAccount {
			debitAbs := amount.Abs()
			if !acct.DailyLimit.IsZero() && acct.DailySpending.Add(debitAbs).GreaterThan(acct.DailyLimit) {
				return fmt.Errorf("limit_exceeded: daily spending limit exceeded on account %s: current %s + debit %s > limit %s",
					accountNumber, acct.DailySpending.StringFixed(4), debitAbs.StringFixed(4), acct.DailyLimit.StringFixed(4))
			}
			if !acct.MonthlyLimit.IsZero() && acct.MonthlySpending.Add(debitAbs).GreaterThan(acct.MonthlyLimit) {
				return fmt.Errorf("limit_exceeded: monthly spending limit exceeded on account %s: current %s + debit %s > limit %s",
					accountNumber, acct.MonthlySpending.StringFixed(4), debitAbs.StringFixed(4), acct.MonthlyLimit.StringFixed(4))
			}
		}

		// Check sufficient funds for debits.
		if isDebit && acct.AvailableBalance.Add(amount).IsNegative() {
			return fmt.Errorf("insufficient funds on account %s: available %s, debit %s",
				accountNumber, acct.AvailableBalance.StringFixed(4), amount.Abs().StringFixed(4))
		}

		// Build the update map and apply atomically.
		updates := map[string]interface{}{
			"balance": gorm.Expr("balance + ?", amount),
		}
		if updateAvailable {
			updates["available_balance"] = gorm.Expr("available_balance + ?", amount)
		}
		if isDebit && !acct.IsBankAccount {
			debitAbs := amount.Abs()
			updates["daily_spending"] = gorm.Expr("daily_spending + ?", debitAbs)
			updates["monthly_spending"] = gorm.Expr("monthly_spending + ?", debitAbs)
		}

		result := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.Account{}).Where("id = ?", acct.ID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// UpdateSpending increments daily_spending and monthly_spending by the given amount.
// Only call this for debit operations on client accounts (not bank accounts).
func (r *AccountRepository) UpdateSpending(accountNumber string, amount decimal.Decimal) error {
	result := r.db.Session(&gorm.Session{SkipHooks: true}).Model(&model.Account{}).
		Where("account_number = ? AND is_bank_account = ?", accountNumber, false).
		Updates(map[string]interface{}{
			"daily_spending":   gorm.Expr("daily_spending + ?", amount),
			"monthly_spending": gorm.Expr("monthly_spending + ?", amount),
		})
	return result.Error
}
