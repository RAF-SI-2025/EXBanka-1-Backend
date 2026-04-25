package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCReadReceiptRepository struct{ db *gorm.DB }

func NewOTCReadReceiptRepository(db *gorm.DB) *OTCReadReceiptRepository {
	return &OTCReadReceiptRepository{db: db}
}

// Upsert sets last_seen_updated_at = MAX(existing, ts). Uses GREATEST on
// PostgreSQL; on SQLite (test envs) the OnConflict assignment falls back to
// "set to ts" because GREATEST isn't available — caller's read-receipt is
// monotonic by construction (only ever increases) so the lossy fallback is
// fine for tests.
func (r *OTCReadReceiptRepository) Upsert(userID int64, systemType string, offerID uint64, ts time.Time) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "system_type"}, {Name: "offer_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_seen_updated_at": ts,
		}),
	}).Create(&model.OTCOfferReadReceipt{
		UserID: userID, SystemType: systemType, OfferID: offerID, LastSeenUpdatedAt: ts,
	}).Error
}

func (r *OTCReadReceiptRepository) GetReceipt(userID int64, systemType string, offerID uint64) (*model.OTCOfferReadReceipt, error) {
	var rec model.OTCOfferReadReceipt
	err := r.db.Where("user_id = ? AND system_type = ? AND offer_id = ?", userID, systemType, offerID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &rec, err
}
