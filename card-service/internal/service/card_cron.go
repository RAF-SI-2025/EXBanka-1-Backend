package service

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/cronreg"
	shared "github.com/exbanka/contract/shared"
)

// StartCardCron launches the card maintenance loop. It exits when ctx is cancelled.
// Each tick is gated by the cronreg Entry so the job can be paused / manually
// triggered via the AdminCron gRPC API.
func StartCardCron(ctx context.Context, cardRepo *repository.CardRepository, blockRepo *repository.CardBlockRepository, db *gorm.DB, registry *cronreg.Registry) {
	entry := registry.Register("card-maintenance", "Unblock expired temp-blocks and deactivate expired virtual cards (every 1 min)", time.Minute)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !entry.BeginRun() {
					continue
				}
				runCardCronTick(ctx, cardRepo, blockRepo, db)
				entry.EndRun(nil)
			case <-entry.TriggerChan():
				if !entry.BeginRun() {
					continue
				}
				runCardCronTick(ctx, cardRepo, blockRepo, db)
				entry.EndRun(nil)
			}
		}
	}()
}

func runCardCronTick(ctx context.Context, cardRepo *repository.CardRepository, blockRepo *repository.CardBlockRepository, db *gorm.DB) {
	now := time.Now()

	// Atomically deactivate expired blocks AND unblock the card in one transaction.
	blocks, err := blockRepo.FindExpiredActive(now)
	if err != nil {
		log.Printf("card cron: failed to fetch expired blocks: %v", err)
	}
	for _, block := range blocks {
		blockID := block.ID
		cardID := block.CardID
		if txErr := db.Transaction(func(tx *gorm.DB) error {
			if e := tx.Model(&block).Where("id = ?", blockID).Update("active", false).Error; e != nil {
				return e
			}
			card, e := cardRepo.GetByIDForUpdate(tx, cardID)
			if e != nil {
				return e
			}
			card.Status = "active"
			return shared.CheckRowsAffected(tx.Save(card))
		}); txErr != nil {
			log.Printf("card cron: failed to unblock card %d (block %d): %v", cardID, blockID, txErr)
		}
	}

	// Deactivate expired virtual cards.
	expired, err := cardRepo.FindExpiredVirtual(now)
	if err != nil {
		log.Printf("card cron: failed to fetch expired virtual cards: %v", err)
	}
	for _, c := range expired {
		card := c
		card.Status = "deactivated"
		if txErr := db.Transaction(func(tx *gorm.DB) error {
			locked, e := cardRepo.GetByIDForUpdate(tx, card.ID)
			if e != nil {
				return e
			}
			if locked.Status == "deactivated" {
				return nil // already done by concurrent tick
			}
			locked.Status = "deactivated"
			return shared.CheckRowsAffected(tx.Save(locked))
		}); txErr != nil {
			log.Printf("card cron: failed to deactivate virtual card %d: %v", card.ID, txErr)
		}
	}
	_ = ctx
}
