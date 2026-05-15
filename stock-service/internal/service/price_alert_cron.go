package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// PriceAlertCron periodically scans active price alerts and evaluates each
// against the listing's current price. Reactive evaluation from the
// price-refresh path is preferable but the refresh code touches multiple
// surfaces (simulator, external, generated); a cron is simpler to wire
// and keeps the alert flow self-contained.
type PriceAlertCron struct {
	alertSvc    *PriceAlertService
	listingRepo ListingRepo
	repo        *repository.PriceAlertRepository
	interval    time.Duration
}

func NewPriceAlertCron(alertSvc *PriceAlertService, listings ListingRepo, repo *repository.PriceAlertRepository, interval time.Duration) *PriceAlertCron {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &PriceAlertCron{alertSvc: alertSvc, listingRepo: listings, repo: repo, interval: interval}
}

// Run blocks until ctx is cancelled; evaluates every active alert on each tick.
func (c *PriceAlertCron) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick(ctx)
		}
	}
}

func (c *PriceAlertCron) tick(ctx context.Context) {
	// Group active alerts by listing_id so we evaluate each listing once.
	// O(n) over all active alerts; with small N this is cheaper than per-
	// alert listing fetches.
	// Use the repo via ListByOwner with each known owner type is wasteful.
	// Cheaper: scan distinct listing_ids directly.
	var rows []model.PriceAlert
	if err := c.repo.DB().Where("active = ?", true).Find(&rows).Error; err != nil {
		log.Printf("WARN: price-alert cron list failed: %v", err)
		return
	}
	listings := make(map[uint64]struct{}, len(rows))
	for _, a := range rows {
		listings[a.ListingID] = struct{}{}
	}
	for lid := range listings {
		listing, err := c.listingRepo.GetByID(lid)
		if err != nil {
			continue
		}
		c.alertSvc.EvaluateForListing(ctx, lid, listing.Price, listing.Change)
	}
}
