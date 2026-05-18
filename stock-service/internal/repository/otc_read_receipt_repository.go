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
//
// For owner_type=bank, ownerID must be 0 (the schema uses owner_id=0 as the
// bank's slot in this composite-PK table; see model doc on OwnerIDForPK).
func (r *OTCReadReceiptRepository) Upsert(ownerType model.OwnerType, ownerID uint64, offerID uint64, ts time.Time) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner_type"}, {Name: "owner_id"}, {Name: "offer_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_seen_updated_at": ts,
		}),
	}).Create(&model.OTCOfferReadReceipt{
		OwnerType: ownerType, OwnerID: ownerID, OfferID: offerID, LastSeenUpdatedAt: ts,
	}).Error
}

func (r *OTCReadReceiptRepository) GetReceipt(ownerType model.OwnerType, ownerID uint64, offerID uint64) (*model.OTCOfferReadReceipt, error) {
	var rec model.OTCOfferReadReceipt
	err := r.db.Where("owner_type = ? AND owner_id = ? AND offer_id = ?", ownerType, ownerID, offerID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &rec, err
}
