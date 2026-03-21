package repository

import (
	"errors"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type AccountRepository struct {
	db *gorm.DB
}

func NewAccountRepository(db *gorm.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) Create(a *model.Account) error {
	return r.db.Create(a).Error
}

func (r *AccountRepository) GetByID(id int64, a *model.Account) error {
	return r.db.First(a, id).Error
}

func (r *AccountRepository) GetByEmail(email string) (*model.Account, error) {
	var a model.Account
	if err := r.db.Where("email = ?", email).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *AccountRepository) GetByPrincipal(principalType string, principalID int64) (*model.Account, error) {
	var a model.Account
	if err := r.db.Where("principal_type = ? AND principal_id = ?", principalType, principalID).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetByPrincipals returns a map of principalID → *Account for batch status lookups.
func (r *AccountRepository) GetByPrincipals(principalType string, principalIDs []int64) (map[int64]*model.Account, error) {
	if len(principalIDs) == 0 {
		return map[int64]*model.Account{}, nil
	}
	var accounts []model.Account
	err := r.db.Where("principal_type = ? AND principal_id IN ?", principalType, principalIDs).Find(&accounts).Error
	if err != nil {
		return nil, err
	}
	m := make(map[int64]*model.Account, len(accounts))
	for i := range accounts {
		m[accounts[i].PrincipalID] = &accounts[i]
	}
	return m, nil
}

func (r *AccountRepository) SetPassword(accountID int64, hash string) error {
	return r.db.Model(&model.Account{}).Where("id = ?", accountID).Update("password_hash", hash).Error
}

// SetPasswordAndActivate atomically sets password hash and changes status to "active" in one UPDATE.
func (r *AccountRepository) SetPasswordAndActivate(accountID int64, hash string) error {
	result := r.db.Model(&model.Account{}).
		Where("id = ? AND status = ?", accountID, model.AccountStatusPending).
		Updates(map[string]interface{}{
			"password_hash": hash,
			"status":        model.AccountStatusActive,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("account not found or not in pending status")
	}
	return nil
}

func (r *AccountRepository) SetStatus(accountID int64, status string) error {
	return r.db.Model(&model.Account{}).Where("id = ?", accountID).Update("status", status).Error
}

func (r *AccountRepository) SetStatusByPrincipal(principalType string, principalID int64, status string) error {
	result := r.db.Model(&model.Account{}).
		Where("principal_type = ? AND principal_id = ?", principalType, principalID).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("account not found")
	}
	return nil
}
