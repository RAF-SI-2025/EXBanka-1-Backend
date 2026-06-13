package repository

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

// WatchlistRepository owns the watchlists + watchlist_items tables.
type WatchlistRepository struct {
	db *gorm.DB
}

func NewWatchlistRepository(db *gorm.DB) *WatchlistRepository {
	return &WatchlistRepository{db: db}
}

// ── Named lists (SP6) ──────────────────────────────────────────────────────

// CreateWatchlist inserts a named list, idempotent on (owner, name): if the
// name already exists for the owner the existing row is returned unchanged.
//
// NULL-owner (bank) correctness: Postgres and SQLite treat NULLs as DISTINCT
// in unique indexes, so ON CONFLICT DO NOTHING never fires for bank-owned rows
// — every call would insert a new duplicate. We guard against this by checking
// existence FIRST via getWatchlistByOwnerName (which emits owner_id IS NULL
// for nil owners and therefore finds existing bank rows correctly).
func (r *WatchlistRepository) CreateWatchlist(w *model.Watchlist) error {
	// Check existence first — handles the NULL-owner (bank) case where the
	// unique index cannot deduplicate (NULL IS DISTINCT FROM NULL in indexes).
	if existing, err := r.getWatchlistByOwnerName(w.OwnerType, w.OwnerID, w.Name); err == nil {
		*w = *existing
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(w).Error; err != nil {
		return err
	}
	if w.ID == 0 {
		// Conflict on a non-NULL owner (race between two concurrent callers) →
		// re-read the winner's row.
		existing, err := r.getWatchlistByOwnerName(w.OwnerType, w.OwnerID, w.Name)
		if err != nil {
			return err
		}
		*w = *existing
	}
	return nil
}

func (r *WatchlistRepository) getWatchlistByOwnerName(ownerType model.OwnerType, ownerID *uint64, name string) (*model.Watchlist, error) {
	var w model.Watchlist
	q := scopeOwner(r.db, "owner_type", "owner_id", ownerType, ownerID).Where("name = ?", name)
	if err := q.First(&w).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

// GetOrCreateDefault returns the owner's default ("My Watchlist") list,
// creating it on first use. Race-safe via the (owner, name) unique index.
func (r *WatchlistRepository) GetOrCreateDefault(ownerType model.OwnerType, ownerID *uint64) (*model.Watchlist, error) {
	w := &model.Watchlist{OwnerType: ownerType, OwnerID: ownerID, Name: model.DefaultWatchlistName}
	if err := r.CreateWatchlist(w); err != nil {
		return nil, err
	}
	return w, nil
}

// GetWatchlist returns a list by id (without ownership scoping — the service
// verifies ownership).
func (r *WatchlistRepository) GetWatchlist(id uint64) (*model.Watchlist, error) {
	var w model.Watchlist
	if err := r.db.First(&w, id).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

// WatchlistWithCount is a named list plus its item count.
type WatchlistWithCount struct {
	model.Watchlist
	ItemCount int64
}

// ListWatchlists returns an owner's named lists with item counts, newest first.
func (r *WatchlistRepository) ListWatchlists(ownerType model.OwnerType, ownerID *uint64) ([]WatchlistWithCount, error) {
	var lists []model.Watchlist
	q := scopeOwner(r.db.Model(&model.Watchlist{}), "owner_type", "owner_id", ownerType, ownerID).Order("created_at ASC, id ASC")
	if err := q.Find(&lists).Error; err != nil {
		return nil, err
	}
	out := make([]WatchlistWithCount, len(lists))
	for i := range lists {
		var n int64
		r.db.Model(&model.WatchlistItem{}).Where("watchlist_id = ?", lists[i].ID).Count(&n)
		out[i] = WatchlistWithCount{Watchlist: lists[i], ItemCount: n}
	}
	return out, nil
}

// DeleteWatchlist removes a list and all its items in one transaction. Returns
// whether the parent row existed.
func (r *WatchlistRepository) DeleteWatchlist(id uint64) (bool, error) {
	removed := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("watchlist_id = ?", id).Delete(&model.WatchlistItem{}).Error; err != nil {
			return err
		}
		res := tx.Delete(&model.Watchlist{}, id)
		if res.Error != nil {
			return res.Error
		}
		removed = res.RowsAffected > 0
		return nil
	})
	return removed, err
}

// ── Items (scoped to a named list) ─────────────────────────────────────────

// Add inserts a watchlist item; idempotent via ON CONFLICT DO NOTHING on
// (watchlist_id, listing_id).
func (r *WatchlistRepository) Add(item *model.WatchlistItem) error {
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

// RemoveFromList deletes the (watchlist_id, listing_id) row; returns whether a
// row was actually removed so the service can map to 404.
func (r *WatchlistRepository) RemoveFromList(watchlistID, listingID uint64) (bool, error) {
	res := r.db.Where("watchlist_id = ? AND listing_id = ?", watchlistID, listingID).Delete(&model.WatchlistItem{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListWithListingsByWatchlist returns a named list's rows joined to their
// listings. listingType filters by listing.security_type when non-empty.
func (r *WatchlistRepository) ListWithListingsByWatchlist(watchlistID uint64, listingType string) ([]WatchlistWithListing, error) {
	q := r.db.Model(&model.WatchlistItem{}).Where("watchlist_items.watchlist_id = ?", watchlistID)
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

// ListAllClientWatchlistItems returns every watchlist item owned by a client
// (owner_type='client', owner_id IS NOT NULL) joined to its listing. Used
// exclusively by the daily watchlist notification cron, which scans all users
// at once. Unchanged by SP6 — items still carry owner_type/owner_id.
func (r *WatchlistRepository) ListAllClientWatchlistItems(listingType string) ([]WatchlistWithListing, error) {
	q := r.db.Model(&model.WatchlistItem{}).
		Where("watchlist_items.owner_type = ? AND watchlist_items.owner_id IS NOT NULL", string(model.OwnerClient)).
		Joins("JOIN listings ON listings.id = watchlist_items.listing_id")
	if listingType != "" {
		q = q.Where("listings.security_type = ?", listingType)
	}
	q = q.Order("watchlist_items.owner_id, watchlist_items.listing_id")

	var rows []WatchlistWithListing
	if err := q.Select(
		"watchlist_items.id AS id",
		"watchlist_items.listing_id AS listing_id",
		"watchlist_items.owner_id AS owner_id",
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

// WatchlistWithListing is the projection returned by the list queries —
// flattened to keep service-layer enrichment simple. OwnerID is populated only
// by ListAllClientWatchlistItems (nil in the per-list query path).
type WatchlistWithListing struct {
	ID           uint64
	ListingID    uint64
	OwnerID      *uint64
	AddedAt      time.Time
	SecurityType string
	SecurityID   uint64
	Price        decimal.Decimal
	DailyChange  decimal.Decimal
}
