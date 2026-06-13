package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/repository"
)

// watchlistAlertThreshold is the minimum absolute daily-change-percent that
// triggers a watchlist notification. Configurable via the cron factory but
// not exposed as an env-var: 5% is the spec value.
const watchlistAlertThreshold = 5.0

// watchlistAlertPublisher is the minimal Kafka interface needed by
// WatchlistNotificationCron so tests can inject a recording stub.
type watchlistAlertPublisher interface {
	PublishWatchlistAlert(ctx context.Context, msg kafkamsg.WatchlistPriceMoveMessage) error
}

// WatchlistNotificationCron scans every active watchlist entry once per
// configured interval (default 24 h), and for each (owner, ticker) pair
// whose daily price change exceeds ±watchlistAlertThreshold% it publishes
// exactly one WatchlistPriceMoveMessage per calendar day. Dedup is
// enforced producer-side via an in-memory set keyed on the daily
// idempotency key; a cron restart on the same day will re-generate the
// same keys but the consumer must also be idempotent (it checks the key
// before inserting).
type WatchlistNotificationCron struct {
	watchlistRepo *repository.WatchlistRepository
	stocks        stockTickerLookup
	options       optionTickerLookup
	futures       futuresTickerLookup
	forex         forexTickerLookup
	publisher     watchlistAlertPublisher
	interval      time.Duration
	entry         *cronreg.Entry
}

// NewWatchlistNotificationCron wires and registers the cron. interval
// controls how often the cron runs; the spec default is 24 h (configurable
// via WATCHLIST_NOTIFICATION_CRON_HOURS).
func NewWatchlistNotificationCron(
	watchlistRepo *repository.WatchlistRepository,
	stocks stockTickerLookup,
	options optionTickerLookup,
	futures futuresTickerLookup,
	forex forexTickerLookup,
	publisher watchlistAlertPublisher,
	interval time.Duration,
	registry *cronreg.Registry,
) *WatchlistNotificationCron {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	c := &WatchlistNotificationCron{
		watchlistRepo: watchlistRepo,
		stocks:        stocks,
		options:       options,
		futures:       futures,
		forex:         forex,
		publisher:     publisher,
		interval:      interval,
	}
	c.entry = registry.Register(
		"watchlist-notification",
		"Daily scan: notify users when watchlisted tickers move more than ±5% in a day",
		interval,
	)
	return c
}

// Run blocks until ctx is cancelled, running the alert pass on every tick.
func (c *WatchlistNotificationCron) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !c.entry.BeginRun() {
				continue
			}
			c.tick(ctx)
			c.entry.EndRun(nil)
		case <-c.entry.TriggerChan():
			if !c.entry.BeginRun() {
				continue
			}
			c.tick(ctx)
			c.entry.EndRun(nil)
		}
	}
}

func (c *WatchlistNotificationCron) tick(ctx context.Context) {
	// All client watchlist items — scan all users at once.
	// ListAllClientWatchlistItems joins listing prices and daily_change.
	rows, err := c.watchlistRepo.ListAllClientWatchlistItems("")
	if err != nil {
		log.Printf("WARN: watchlist-notification cron: list failed: %v", err)
		return
	}

	today := time.Now().UTC().Format("20060102")
	// In-memory dedup so a restart within the same day doesn't re-notify.
	// The consumer also deduplicates; this is a belt-and-suspenders guard.
	sent := make(map[string]struct{}, len(rows))

	threshold := decimal.NewFromFloat(watchlistAlertThreshold)

	for _, row := range rows {
		if row.Price.IsZero() {
			continue // listing has no price yet
		}
		pct := dailyChangePercent(row.Price, row.DailyChange)

		// abs(pct) must exceed the threshold.
		absPct := pct
		if absPct.IsNegative() {
			absPct = absPct.Neg()
		}
		if absPct.LessThan(threshold) {
			continue
		}

		// We only notify client owners (bank rows have no human recipient).
		if row.OwnerID == nil {
			continue
		}
		ownerID := *row.OwnerID

		ticker := resolveTickerForCron(c, row.SecurityType, row.SecurityID)
		if ticker == "" {
			log.Printf("WARN: watchlist-notification cron: could not resolve ticker for listing %d (type=%s, sec=%d)", row.ListingID, row.SecurityType, row.SecurityID)
			continue
		}

		idempKey := fmt.Sprintf("watchlist-alert-%d-%s-%s", ownerID, ticker, today)
		if _, already := sent[idempKey]; already {
			continue
		}
		sent[idempKey] = struct{}{}

		msg := kafkamsg.WatchlistPriceMoveMessage{
			UserID:         ownerID,
			Ticker:         ticker,
			PercentMove:    pct.StringFixed(4),
			CurrentPrice:   row.Price.StringFixed(4),
			Timestamp:      time.Now().UTC(),
			IdempotencyKey: idempKey,
		}
		if err := c.publisher.PublishWatchlistAlert(ctx, msg); err != nil {
			log.Printf("WARN: watchlist-notification cron: publish failed for user %d ticker %s: %v", ownerID, ticker, err)
		} else {
			log.Printf("watchlist-notification cron: notified user %d for %s (pct=%s)", ownerID, ticker, pct.StringFixed(2))
		}
	}
}

// resolveTickerForCron resolves the human-readable ticker for a listing.
// Mirrors WatchlistService.resolveTicker but takes the cron's lookup fields
// directly so there is no circular dependency on WatchlistService.
func resolveTickerForCron(c *WatchlistNotificationCron, securityType string, securityID uint64) string {
	switch securityType {
	case "stock":
		if c.stocks == nil {
			return ""
		}
		st, err := c.stocks.GetByID(securityID)
		if err != nil || st == nil {
			return ""
		}
		return st.Ticker
	case "option":
		if c.options == nil {
			return ""
		}
		o, err := c.options.GetByID(securityID)
		if err != nil || o == nil {
			return ""
		}
		return o.Ticker
	case "futures":
		if c.futures == nil {
			return ""
		}
		f, err := c.futures.GetByID(securityID)
		if err != nil || f == nil {
			return ""
		}
		return f.Ticker
	case "forex":
		if c.forex == nil {
			return ""
		}
		f, err := c.forex.GetByID(securityID)
		if err != nil || f == nil {
			return ""
		}
		return f.Ticker
	}
	return ""
}
