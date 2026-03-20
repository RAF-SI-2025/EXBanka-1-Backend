package repository

import (
	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
	"gorm.io/gorm"
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
	result := r.db.Model(&model.Account{}).Where("id = ? AND owner_id = ?", id, clientID).Update("account_name", newName)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) UpdateLimits(id uint64, updates map[string]interface{}) error {
	result := r.db.Model(&model.Account{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) UpdateStatus(id uint64, status string) error {
	result := r.db.Model(&model.Account{}).Where("id = ?", id).Update("status", status)
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

func (r *AccountRepository) UpdateBalance(accountNumber string, amount decimal.Decimal, updateAvailable bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"balance": gorm.Expr("balance + ?", amount),
		}
		if updateAvailable {
			updates["available_balance"] = gorm.Expr("available_balance + ?", amount)
		}
		result := tx.Model(&model.Account{}).Where("account_number = ?", accountNumber).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}
