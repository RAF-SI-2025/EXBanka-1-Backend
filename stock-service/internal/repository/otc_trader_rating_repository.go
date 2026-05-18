package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

// ErrRatingAlreadyExists — a (offer, rater) pair already has a rating.
// Surfaced as 409 by the gateway.
var ErrRatingAlreadyExists = errors.New("rating already submitted for this offer")

type OTCTraderRatingRepository struct {
	db *gorm.DB
}

func NewOTCTraderRatingRepository(db *gorm.DB) *OTCTraderRatingRepository {
	return &OTCTraderRatingRepository{db: db}
}

// Create persists the rating; returns ErrRatingAlreadyExists when the
// (offer, rater) row already exists. ON CONFLICT DO NOTHING + a
// follow-up check keeps the dedup decision in the DB so concurrent
// submissions don't both succeed.
func (r *OTCTraderRatingRepository) Create(row *model.OTCTraderRating) error {
	res := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRatingAlreadyExists
	}
	return nil
}

// AvgForRated returns the (avg, count) of ratings the rated owner has
// received. Returns (0,0) when no ratings exist.
func (r *OTCTraderRatingRepository) AvgForRated(ratedType model.OwnerType, ratedID *uint64) (float64, int64, error) {
	q := scopeOwner(r.db.Model(&model.OTCTraderRating{}), "rated_owner_type", "rated_owner_id", ratedType, ratedID)
	var row struct {
		Avg   *float64
		Count int64
	}
	if err := q.Select("AVG(score) AS avg, COUNT(*) AS count").Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	if row.Avg == nil {
		return 0, 0, nil
	}
	return *row.Avg, row.Count, nil
}

// ListForRated returns the most recent N ratings the rated owner has
// received. Used for the public profile + the caller's "ratings I
// received" view.
func (r *OTCTraderRatingRepository) ListForRated(ratedType model.OwnerType, ratedID *uint64, limit int) ([]model.OTCTraderRating, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := scopeOwner(r.db.Model(&model.OTCTraderRating{}), "rated_owner_type", "rated_owner_id", ratedType, ratedID)
	var out []model.OTCTraderRating
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
