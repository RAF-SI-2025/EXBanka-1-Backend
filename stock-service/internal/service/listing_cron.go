package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/influx"
	"github.com/exbanka/stock-service/internal/model"
)

type ListingCronService struct {
	listingRepo  ListingRepo
	dailyRepo    DailyPriceRepo
	influxClient *influx.Client
}

func NewListingCronService(listingRepo ListingRepo, dailyRepo DailyPriceRepo, influxClient *influx.Client) *ListingCronService {
	return &ListingCronService{
		listingRepo:  listingRepo,
		dailyRepo:    dailyRepo,
		influxClient: influxClient,
	}
}

// SnapshotDailyPrices takes the current price of every listing and saves it
// as today's daily price entry. Idempotent (upserts by listing+date).
func (c *ListingCronService) SnapshotDailyPrices() {
	listings, err := c.listingRepo.ListAll()
	if err != nil {
		log.Printf("WARN: listing cron: failed to list listings: %v", err)
		return
	}

	today := time.Now().Truncate(24 * time.Hour)
	count := 0
	for _, l := range listings {
		info := &model.ListingDailyPriceInfo{
			ListingID: l.ID,
			Date:      today,
			Price:     l.Price,
			High:      l.High,
			Low:       l.Low,
			Change:    l.Change,
			Volume:    l.Volume,
		}
		if err := c.dailyRepo.UpsertByListingAndDate(info); err != nil {
			log.Printf("WARN: listing cron: failed to snapshot listing %d: %v", l.ID, err)
			continue
		}

		// Dual-write to InfluxDB
		writeSecurityPricePoint(
			c.influxClient, l.ID, l.SecurityType, "", l.Exchange.Acronym,
			l.Price, l.High, l.Low, l.Change, l.Volume,
			today,
		)

		count++
	}
	log.Printf("listing cron: snapshotted %d daily prices for %s", count, today.Format("2006-01-02"))
}

// StartDailyCron schedules the snapshot to run daily at 23:55 (just before midnight).
func (c *ListingCronService) StartDailyCron(ctx context.Context) {
	go func() {
		for {
			now := time.Now()
			// Next run at 23:55 today (or tomorrow if already past)
			next := time.Date(now.Year(), now.Month(), now.Day(), 23, 55, 0, 0, time.UTC)
			if now.After(next) {
				next = next.AddDate(0, 0, 1)
			}
			waitDuration := time.Until(next)

			select {
			case <-time.After(waitDuration):
				log.Println("listing cron: running daily price snapshot")
				c.SnapshotDailyPrices()
			case <-ctx.Done():
				log.Println("listing cron: stopped")
				return
			}
		}
	}()
	log.Println("listing cron: scheduled daily at 23:55")
}

// SeedInitialSnapshot creates today's price snapshot for all listings if no
// history exists yet. Called once at startup after listings are synced.
func (c *ListingCronService) SeedInitialSnapshot() {
	listings, err := c.listingRepo.ListAll()
	if err != nil {
		log.Printf("WARN: listing cron: failed to seed initial snapshot: %v", err)
		return
	}

	today := time.Now().Truncate(24 * time.Hour)
	count := 0
	for _, l := range listings {
		// Check if today's snapshot already exists
		existing, _, _ := c.dailyRepo.GetHistory(l.ID, today, today, 1, 1)
		if len(existing) > 0 {
			continue
		}
		info := &model.ListingDailyPriceInfo{
			ListingID: l.ID,
			Date:      today,
			Price:     l.Price,
			High:      l.High,
			Low:       l.Low,
			Change:    l.Change,
			Volume:    l.Volume,
		}
		if err := c.dailyRepo.UpsertByListingAndDate(info); err != nil {
			log.Printf("WARN: listing cron: failed to seed snapshot for listing %d: %v", l.ID, err)
			continue
		}
		count++
	}
	if count > 0 {
		log.Printf("listing cron: seeded %d initial price snapshots", count)
	}
}
