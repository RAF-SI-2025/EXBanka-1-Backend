package repository

import (
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCOfferRevisionRepository struct{ db *gorm.DB }

func NewOTCOfferRevisionRepository(db *gorm.DB) *OTCOfferRevisionRepository {
	return &OTCOfferRevisionRepository{db: db}
}

func (r *OTCOfferRevisionRepository) Append(rev *model.OTCOfferRevision) error {
	return r.db.Create(rev).Error
}

func (r *OTCOfferRevisionRepository) ListByOffer(offerID uint64) ([]model.OTCOfferRevision, error) {
	var out []model.OTCOfferRevision
	err := r.db.Where("offer_id = ?", offerID).Order("revision_number ASC").Find(&out).Error
	return out, err
}

func (r *OTCOfferRevisionRepository) NextRevisionNumber(offerID uint64) (int, error) {
	var max struct{ N int }
	err := r.db.Raw("SELECT COALESCE(MAX(revision_number), 0) AS n FROM otc_offer_revisions WHERE offer_id = ?", offerID).Scan(&max).Error
	return max.N + 1, err
}
