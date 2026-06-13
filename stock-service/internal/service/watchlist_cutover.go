package service

import (
	"log"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// DedupeWatchlistsAndEnforceUniqueness is an idempotent startup migration that
// fixes the NULL-owner duplicate-watchlist data-integrity bug (see
// WatchlistRepository.CreateWatchlist for the root-cause comment).
//
// It does three things:
//  1. Finds duplicate (owner_type, owner_id, name) groups (NULL owner_id groups
//     correctly because SQL GROUP BY treats NULLs as equal, unlike unique indexes).
//  2. For each group, keeps the MIN(id) row; repoints all items from duplicate
//     rows to the keeper (avoiding watchlist_item unique-index collisions); then
//     deletes the duplicate watchlist rows.
//  3. Creates a partial unique index on (owner_type, name) WHERE owner_id IS NULL
//     so future NULL-owner duplicate inserts are rejected at the DB level. The
//     non-NULL (client) case is already covered by the GORM-generated
//     idx_watchlist_owner_name index.
//
// Safe to call on every startup: GROUP BY finds nothing once clean, IF NOT EXISTS
// makes the index creation idempotent.
func DedupeWatchlistsAndEnforceUniqueness(db *gorm.DB) error {
	// dupGroup carries one row from the GROUP BY query.
	type dupGroup struct {
		OwnerType string
		OwnerID   *uint64
		Name      string
		KeepID    uint64
	}

	// Step 1: Find groups with more than one row. SQL GROUP BY groups NULLs
	// together (unlike unique-index NULL-distinctness), so NULL-owner groups
	// surface correctly here.
	var groups []dupGroup
	if err := db.Raw(`
		SELECT owner_type, owner_id, name, MIN(id) AS keep_id
		FROM watchlists
		GROUP BY owner_type, owner_id, name
		HAVING COUNT(*) > 1
	`).Scan(&groups).Error; err != nil {
		return err
	}

	// Step 2: For each duplicate group, merge items into the keeper and delete
	// the duplicate list rows.
	for _, g := range groups {
		g := g // capture for closure
		if err := db.Transaction(func(tx *gorm.DB) error {
			// Collect dup IDs (same owner+name, not the keeper).
			q := tx.Model(&model.Watchlist{}).
				Where("owner_type = ? AND name = ? AND id <> ?", g.OwnerType, g.Name, g.KeepID)
			if g.OwnerID == nil {
				q = q.Where("owner_id IS NULL")
			} else {
				q = q.Where("owner_id = ?", *g.OwnerID)
			}
			var dupIDs []uint64
			if err := q.Pluck("id", &dupIDs).Error; err != nil {
				return err
			}
			if len(dupIDs) == 0 {
				return nil
			}

			// Process each dup individually so that once a dup's items are
			// merged into the keeper, subsequent dups check the updated keeper
			// — this correctly handles cross-dup listing_id collisions (two
			// different dup lists holding the same listing_id).
			for _, dupID := range dupIDs {
				// Remove items from this dup whose listing_id already exists in
				// the keeper (would violate idx_watchlist_item_list_listing).
				if err := tx.Exec(
					`DELETE FROM watchlist_items
					  WHERE watchlist_id = ?
					    AND listing_id IN (
					          SELECT listing_id FROM watchlist_items WHERE watchlist_id = ?
					        )`,
					dupID, g.KeepID,
				).Error; err != nil {
					return err
				}
				// Repoint remaining items to the keeper. SkipHooks to avoid
				// the BeforeSave owner-validation gotcha on bulk updates
				// (zero-value WatchlistItem would have empty OwnerType).
				if err := tx.Session(&gorm.Session{SkipHooks: true}).
					Exec(`UPDATE watchlist_items SET watchlist_id = ? WHERE watchlist_id = ?`,
						g.KeepID, dupID,
					).Error; err != nil {
					return err
				}
			}

			// Delete the now-empty duplicate watchlist rows.
			if err := tx.Exec(`DELETE FROM watchlists WHERE id IN ?`, dupIDs).Error; err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}

	// Step 3: Create the partial unique index for NULL-owner rows.
	// Runs AFTER dedup so no pre-existing dups can block the index creation.
	// IF NOT EXISTS makes this safe on every subsequent startup call.
	if err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_watchlist_bank_name
		ON watchlists (owner_type, name)
		WHERE owner_id IS NULL
	`).Error; err != nil {
		return err
	}

	return nil
}

// MigrateWatchlistsToNamedLists is a one-time, idempotent startup migration for
// SP6: it drops the legacy per-(owner, listing) unique index and assigns every
// pre-existing watchlist item (watchlist_id = 0) to its owner's default
// "My Watchlist", creating that list lazily. Safe to run on every startup —
// once all items have a watchlist_id, the backfill loop finds nothing.
func MigrateWatchlistsToNamedLists(db *gorm.DB, repo *repository.WatchlistRepository) error {
	// Drop the old unique index so the same listing may live in multiple named
	// lists (the new unique index is on (watchlist_id, listing_id)).
	if err := db.Exec("DROP INDEX IF EXISTS idx_watchlist_owner_listing").Error; err != nil {
		log.Printf("WARN: watchlist migration: drop legacy index: %v", err)
	}

	type ownerRow struct {
		OwnerType string
		OwnerID   *uint64
	}
	var owners []ownerRow
	if err := db.Model(&model.WatchlistItem{}).
		Where("watchlist_id = 0 OR watchlist_id IS NULL").
		Distinct("owner_type", "owner_id").
		Find(&owners).Error; err != nil {
		return err
	}
	for _, o := range owners {
		def, err := repo.GetOrCreateDefault(model.OwnerType(o.OwnerType), o.OwnerID)
		if err != nil {
			log.Printf("WARN: watchlist migration: default list for %s/%v: %v", o.OwnerType, o.OwnerID, err)
			continue
		}
		// SkipHooks: a bulk Update builds a zero-value WatchlistItem whose
		// BeforeSave would reject the empty owner_type (CLAUDE.md bulk-update
		// gotcha). We're only setting watchlist_id, so the owner validation is
		// irrelevant here.
		q := db.Session(&gorm.Session{SkipHooks: true}).Model(&model.WatchlistItem{}).
			Where("owner_type = ? AND (watchlist_id = 0 OR watchlist_id IS NULL)", o.OwnerType)
		if o.OwnerID == nil {
			q = q.Where("owner_id IS NULL")
		} else {
			q = q.Where("owner_id = ?", *o.OwnerID)
		}
		if err := q.Update("watchlist_id", def.ID).Error; err != nil {
			log.Printf("WARN: watchlist migration: backfill items for %s/%v: %v", o.OwnerType, o.OwnerID, err)
		}
	}
	return nil
}
