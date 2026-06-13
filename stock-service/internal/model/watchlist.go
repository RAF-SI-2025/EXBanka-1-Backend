package model

import (
	"time"

	"gorm.io/gorm"
)

// Watchlist is a named collection of tracked listings owned by a client or the
// bank (SP6 / Celina 3). A user may keep several (e.g. "tech stocks", "forex
// pairs"). Unique per (owner, name). Items live in watchlist_items with a
// watchlist_id FK.
type Watchlist struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType OwnerType `gorm:"type:varchar(16);not null;uniqueIndex:idx_watchlist_owner_name,priority:1" json:"owner_type"`
	OwnerID   *uint64   `gorm:"uniqueIndex:idx_watchlist_owner_name,priority:2" json:"owner_id,omitempty"`
	Name      string    `gorm:"size:64;not null;uniqueIndex:idx_watchlist_owner_name,priority:3" json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DefaultWatchlistName is the lazily-created list the legacy single-list
// endpoints operate on, so existing clients keep working unchanged.
const DefaultWatchlistName = "My Watchlist"

func (w *Watchlist) BeforeSave(*gorm.DB) error {
	return ValidateOwner(w.OwnerType, w.OwnerID)
}
