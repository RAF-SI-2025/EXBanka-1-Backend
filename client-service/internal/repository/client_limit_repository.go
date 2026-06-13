package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
// Uses ON CONFLICT DO UPDATE to eliminate the TOCTOU race between SELECT and INSERT.
// Version is incremented on every conflict-update so SP-5 account-propagation
// events can be ordered correctly (same pattern as EmployeeLimit SP-2 fix).
func (r *ClientLimitRepository) Upsert(limit *model.ClientLimit) error {
	updates := clause.AssignmentColumns([]string{
		"daily_limit", "monthly_limit", "transfer_limit", "set_by_employee", "updated_at",
	})
	// Monotonic version: `version + 1` in ON CONFLICT DO UPDATE refers to the
	// existing row value (Postgres and SQLite both support this expression).
	updates = append(updates, clause.Assignment{
		Column: clause.Column{Name: "version"},
		Value:  gorm.Expr("version + 1"),
	})
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}},
		DoUpdates: updates,
	}).Create(limit).Error
}
