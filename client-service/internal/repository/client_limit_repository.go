package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/model"
	"github.com/shopspring/decimal"
)

// ClientLimitRepository handles persistence for ClientLimit records.
type ClientLimitRepository struct {
	db *gorm.DB
}

func NewClientLimitRepository(db *gorm.DB) *ClientLimitRepository {
	return &ClientLimitRepository{db: db}
}

// GetByClientID returns the limit for a client. Returns a zero-value limit (not an error) if none is configured.
func (r *ClientLimitRepository) GetByClientID(clientID int64) (*model.ClientLimit, error) {
	var limit model.ClientLimit
	err := r.db.Where("client_id = ?", clientID).First(&limit).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &model.ClientLimit{
				ClientID:      clientID,
				DailyLimit:    decimal.NewFromInt(100000),
				MonthlyLimit:  decimal.NewFromInt(1000000),
				TransferLimit: decimal.NewFromInt(50000),
			}, nil
		}
		return nil, err
	}
	return &limit, nil
}

// Upsert creates or updates the client limit for a client.
func (r *ClientLimitRepository) Upsert(limit *model.ClientLimit) error {
	var existing model.ClientLimit
	err := r.db.Where("client_id = ?", limit.ClientID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(limit).Error
		}
		return err
	}
	limit.ID = existing.ID
	limit.CreatedAt = existing.CreatedAt
	return r.db.Save(limit).Error
}
