package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/card-service/internal/repository"
)

func StartCardCron(cardRepo *repository.CardRepository, blockRepo *repository.CardBlockRepository) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			now := time.Now()
			ctx := context.Background()

			// Unblock cards whose temporary block has expired
			blocks, err := blockRepo.FindExpiredActive(now)
			if err != nil {
				log.Printf("card cron: failed to fetch expired blocks: %v", err)
			}
			for _, block := range blocks {
				if err := blockRepo.Deactivate(block.ID); err != nil {
					log.Printf("card cron: failed to deactivate block %d: %v", block.ID, err)
					continue
				}
				if _, err := cardRepo.UpdateStatus(block.CardID, "active"); err != nil {
					log.Printf("card cron: failed to unblock card %d: %v", block.CardID, err)
				}
				_ = ctx
			}

			// Deactivate expired virtual cards
			expired, err := cardRepo.FindExpiredVirtual(now)
			if err != nil {
				log.Printf("card cron: failed to fetch expired virtual cards: %v", err)
			}
			for _, card := range expired {
				card := card
				card.Status = "deactivated"
				if err := cardRepo.Update(&card); err != nil {
					log.Printf("card cron: failed to deactivate virtual card %d: %v", card.ID, err)
				}
			}
		}
	}()
}
