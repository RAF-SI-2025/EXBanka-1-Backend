package model

import (
	"time"

	"gorm.io/gorm"
)

// WatchlistItem is one tracked listing per (owner_type, owner_id). Unique
// per owner+listing pair. No version column; the row is either present or
// absent — no in-place edits.
type WatchlistItem struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType OwnerType `gorm:"type:varchar(16);not null;uniqueIndex:idx_watchlist_owner_listing,priority:1" json:"owner_type"`
	OwnerID   *uint64   `gorm:"uniqueIndex:idx_watchlist_owner_listing,priority:2" json:"owner_id,omitempty"`
	ListingID uint64    `gorm:"not null;uniqueIndex:idx_watchlist_owner_listing,priority:3" json:"listing_id"`
	AddedAt   time.Time `gorm:"not null" json:"added_at"`
}

func (w *WatchlistItem) BeforeSave(*gorm.DB) error {
	if w.AddedAt.IsZero() {
		w.AddedAt = time.Now().UTC()
	}
	return ValidateOwner(w.OwnerType, w.OwnerID)
}
