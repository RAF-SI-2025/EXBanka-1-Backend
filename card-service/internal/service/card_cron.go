package service

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/repository"
	shared "github.com/exbanka/contract/shared"
)

// StartCardCron launches the card maintenance loop. It exits when ctx is cancelled.
func StartCardCron(ctx context.Context, cardRepo *repository.CardRepository, blockRepo *repository.CardBlockRepository, db *gorm.DB) {
	shared.RunScheduled(ctx, shared.ScheduledJob{
		Name:     "card-maintenance",
		Interval: 1 * time.Minute,
		OnTick: func(ctx context.Context) error {
			runCardCronTick(ctx, cardRepo, blockRepo, db)
			return nil
		},
	})
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
