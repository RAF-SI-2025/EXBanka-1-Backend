package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// OTCTraderRating is a star rating (1..5) one party leaves the other
// after a terminally-accepted OTC offer. Each (offer, rater) pair is
// unique so a side can rate at most once per offer.
type OTCTraderRating struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OfferID        uint64    `gorm:"not null;uniqueIndex:idx_rating_offer_rater,priority:1" json:"offer_id"`
	RaterOwnerType OwnerType `gorm:"type:varchar(16);not null;uniqueIndex:idx_rating_offer_rater,priority:2" json:"rater_owner_type"`
	RaterOwnerID   *uint64   `gorm:"uniqueIndex:idx_rating_offer_rater,priority:3" json:"rater_owner_id,omitempty"`
	RatedOwnerType OwnerType `gorm:"type:varchar(16);not null;index:idx_rating_rated,priority:1" json:"rated_owner_type"`
	RatedOwnerID   *uint64   `gorm:"index:idx_rating_rated,priority:2" json:"rated_owner_id,omitempty"`
	Score          int       `gorm:"not null" json:"score"`
	Comment        string    `gorm:"type:text" json:"comment"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
}

func (r *OTCTraderRating) BeforeSave(*gorm.DB) error {
	if r.Score < 1 || r.Score > 5 {
		return errors.New("score must be 1..5")
	}
	if err := ValidateOwner(r.RaterOwnerType, r.RaterOwnerID); err != nil {
		return err
	}
	if err := ValidateOwner(r.RatedOwnerType, r.RatedOwnerID); err != nil {
		return err
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	return nil
}
