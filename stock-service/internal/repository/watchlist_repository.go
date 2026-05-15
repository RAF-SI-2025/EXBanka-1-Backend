package repository

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

// WatchlistRepository owns the watchlist_items table.
type WatchlistRepository struct {
	db *gorm.DB
}

func NewWatchlistRepository(db *gorm.DB) *WatchlistRepository {
	return &WatchlistRepository{db: db}
}

// Add inserts a watchlist row; idempotent via ON CONFLICT DO NOTHING so
// double-adds don't error.
func (r *WatchlistRepository) Add(item *model.WatchlistItem) error {
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

// Remove deletes the row; returns whether a row was actually removed so
// the service can map to 404 when needed.
func (r *WatchlistRepository) Remove(ownerType model.OwnerType, ownerID *uint64, listingID uint64) (bool, error) {
	q := scopeOwner(r.db, "owner_type", "owner_id", ownerType, ownerID)
	res := q.Where("listing_id = ?", listingID).Delete(&model.WatchlistItem{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListWithListings returns watchlist rows joined to their listings (so the
// service layer can compute current-price + daily-change without an N+1
// fetch). listingType filters by listing.security_type when non-empty.
func (r *WatchlistRepository) ListWithListings(ownerType model.OwnerType, ownerID *uint64, listingType string) ([]WatchlistWithListing, error) {
	q := scopeOwner(r.db.Model(&model.WatchlistItem{}), "watchlist_items.owner_type", "watchlist_items.owner_id", ownerType, ownerID)
	q = q.Joins("JOIN listings ON listings.id = watchlist_items.listing_id")
	if listingType != "" {
		q = q.Where("listings.security_type = ?", listingType)
	}
	q = q.Order("watchlist_items.added_at DESC")

	var rows []WatchlistWithListing
	if err := q.Select(
		"watchlist_items.id AS id",
		"watchlist_items.listing_id AS listing_id",
		"watchlist_items.added_at AS added_at",
		"listings.security_type AS security_type",
		"listings.security_id AS security_id",
		"listings.price AS price",
		"listings.change AS daily_change",
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Exists reports whether the (owner, listing) pair is already on the
// watchlist. Useful for handlers that want a precise idempotent yes/no
// rather than just letting Add absorb the conflict.
func (r *WatchlistRepository) Exists(ownerType model.OwnerType, ownerID *uint64, listingID uint64) (bool, error) {
	q := scopeOwner(r.db.Model(&model.WatchlistItem{}), "owner_type", "owner_id", ownerType, ownerID)
	var count int64
	if err := q.Where("listing_id = ?", listingID).Count(&count).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return count > 0, nil
}

// WatchlistWithListing is the projection returned by ListWithListings —
// flattened to keep service-layer enrichment simple. The service maps
// security_id → ticker via the existing security repos.
type WatchlistWithListing struct {
	ID           uint64
	ListingID    uint64
	AddedAt      time.Time
	SecurityType string
	SecurityID   uint64
	Price        decimal.Decimal
	DailyChange  decimal.Decimal
}
