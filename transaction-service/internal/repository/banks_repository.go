package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/transaction-service/internal/model"
)

// BanksRepository persists the peer-bank registry. Rows are seeded on
// transaction-service startup from env-provided peer URLs and HMAC keys.
type BanksRepository struct {
	db *gorm.DB
}

func NewBanksRepository(db *gorm.DB) *BanksRepository {
	return &BanksRepository{db: db}
}

// Upsert idempotently inserts or updates a peer-bank row keyed on `code`.
func (r *BanksRepository) Upsert(b *model.Bank) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "code"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "base_url", "api_key_bcrypted",
			"inbound_api_key_bcrypted", "public_key", "active", "updated_at",
		}),
	}).Create(b).Error
}

// GetByCode looks up a peer by its 3-digit code, returning ErrRecordNotFound
// when absent.
func (r *BanksRepository) GetByCode(code string) (*model.Bank, error) {
	var b model.Bank
	err := r.db.First(&b, "code = ?", code).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &b, err
}

// ListActive returns all active peer banks ordered by code.
func (r *BanksRepository) ListActive() ([]model.Bank, error) {
	var out []model.Bank
	err := r.db.Where("active = ?", true).Order("code ASC").Find(&out).Error
	return out, err
}
