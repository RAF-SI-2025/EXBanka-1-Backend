package model

import (
	"time"

	"gorm.io/gorm"
)

// WatchlistItem is one tracked listing inside a named Watchlist (SP6). Unique
// per (watchlist_id, listing_id) — the same listing may live in different named
// lists. owner_type/owner_id are denormalised from the parent list so the daily
// notification cron can scan per-owner without a join. No version column; the
// row is either present or absent.
type WatchlistItem struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	WatchlistID uint64    `gorm:"not null;default:0;uniqueIndex:idx_watchlist_item_list_listing,priority:1;index" json:"watchlist_id"`
	OwnerType   OwnerType `gorm:"type:varchar(16);not null;index:idx_watchlist_item_owner,priority:1" json:"owner_type"`
	OwnerID     *uint64   `gorm:"index:idx_watchlist_item_owner,priority:2" json:"owner_id,omitempty"`
	ListingID   uint64    `gorm:"not null;uniqueIndex:idx_watchlist_item_list_listing,priority:2" json:"listing_id"`
	AddedAt     time.Time `gorm:"not null" json:"added_at"`
}

func (w *WatchlistItem) BeforeSave(*gorm.DB) error {
	if w.AddedAt.IsZero() {
		w.AddedAt = time.Now().UTC()
	}
	return ValidateOwner(w.OwnerType, w.OwnerID)
}
