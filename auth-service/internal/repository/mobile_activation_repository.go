package repository

import (
	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MobileActivationRepository struct {
	db *gorm.DB
}

func NewMobileActivationRepository(db *gorm.DB) *MobileActivationRepository {
	return &MobileActivationRepository{db: db}
}

func (r *MobileActivationRepository) Create(code *model.MobileActivationCode) error {
	return r.db.Create(code).Error
}

func (r *MobileActivationRepository) GetLatestByEmail(email string) (*model.MobileActivationCode, error) {
	var code model.MobileActivationCode
	if err := r.db.Where("email = ? AND used = false", email).
		Order("id DESC").First(&code).Error; err != nil {
		return nil, err
	}
	return &code, nil
}

// GetLatestByEmailForUpdate retrieves the latest activation code with a SELECT FOR UPDATE lock.
func (r *MobileActivationRepository) GetLatestByEmailForUpdate(tx *gorm.DB, email string) (*model.MobileActivationCode, error) {
	var code model.MobileActivationCode
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("email = ? AND used = false", email).
		Order("id DESC").First(&code).Error; err != nil {
		return nil, err
	}
	return &code, nil
}

func (r *MobileActivationRepository) IncrementAttempts(id int64) error {
	return r.db.Model(&model.MobileActivationCode{}).
		Where("id = ?", id).
		Update("attempts", gorm.Expr("attempts + 1")).Error
}

// IncrementAttemptsInTx increments attempts within an existing transaction.
func (r *MobileActivationRepository) IncrementAttemptsInTx(tx *gorm.DB, id int64) error {
	return tx.Model(&model.MobileActivationCode{}).
		Where("id = ?", id).
		Update("attempts", gorm.Expr("attempts + 1")).Error
}

// MarkUsedInTx marks the activation code as used within an existing transaction.
func (r *MobileActivationRepository) MarkUsedInTx(tx *gorm.DB, id int64) error {
	return tx.Model(&model.MobileActivationCode{}).
		Where("id = ?", id).
		Update("used", true).Error
}

func (r *MobileActivationRepository) MarkUsed(id int64) error {
	return r.db.Model(&model.MobileActivationCode{}).
		Where("id = ?", id).
		Update("used", true).Error
}

// DB returns the underlying gorm.DB for transaction use.
func (r *MobileActivationRepository) DB() *gorm.DB {
	return r.db
}
