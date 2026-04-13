package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/influx"
	"github.com/exbanka/stock-service/internal/cache"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/source"
)

// SupportedCurrencies matches the 8 currencies supported by exchange-service.
var SupportedCurrencies = []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

type SecuritySyncService struct {
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
	optionRepo   OptionRepo
	exchangeRepo *repository.ExchangeRepository
	settingRepo  SettingRepo
	listingSvc   *ListingService
	cache        *cache.RedisCache
	influxClient *influx.Client

	// Provider clients are kept for the periodic refresh loop only.
	// syncStockPrices and refreshForexRates call the external APIs directly
	// because Source.RefreshPrices is a no-op on ExternalSource.
	avClient      *provider.AlphaVantageClient
	finnhubClient *provider.FinnhubClient

	srcMu sync.RWMutex
	src   source.Source

	// Switch orchestration state.
	wipe       Wiper
	switchMu   sync.Mutex
	statusMu   sync.RWMutex
	status     string             // "idle" | "reseeding" | "failed"
	lastErr    string
	startedAt  time.Time
	refreshCtx context.CancelFunc // non-nil while the simulator refresh loop is running
}

func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo *repository.ExchangeRepository,
	settingRepo SettingRepo,
	listingSvc *ListingService,
	redisCache *cache.RedisCache,
	influxClient *influx.Client,
	avClient *provider.AlphaVantageClient,
	finnhubClient *provider.FinnhubClient,
	initialSource source.Source,
	wipe Wiper,
) *SecuritySyncService {
	return &SecuritySyncService{
		stockRepo:     stockRepo,
		futuresRepo:   futuresRepo,
		forexRepo:     forexRepo,
		optionRepo:    optionRepo,
		exchangeRepo:  exchangeRepo,
		settingRepo:   settingRepo,
		listingSvc:    listingSvc,
		cache:         redisCache,
		influxClient:  influxClient,
		avClient:      avClient,
		finnhubClient: finnhubClient,
		src:           initialSource,
		wipe:          wipe,
		status:        "idle",
	}
}

// Source returns the currently active data source (concurrency-safe).
func (s *SecuritySyncService) Source() source.Source {
	s.srcMu.RLock()
	defer s.srcMu.RUnlock()
	return s.src
}

// SetSource replaces the active data source (concurrency-safe).
func (s *SecuritySyncService) SetSource(newSrc source.Source) {
	s.srcMu.Lock()
	defer s.srcMu.Unlock()
	s.src = newSrc
}

// SeedAll runs the full initial data seed.
func (s *SecuritySyncService) SeedAll(ctx context.Context, futuresSeedPath string) {
	s.syncExchanges()
	s.syncStocks(ctx)
	s.seedForexPairs()
	s.seedFutures(futuresSeedPath)
	s.generateAllOptions()
	// Sync listings from the securities we just seeded
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
}

// RefreshPrices updates price data for all securities.
// Called periodically by the refresh goroutine.
func (s *SecuritySyncService) RefreshPrices(ctx context.Context) {
	start := time.Now()

	if s.isTestingMode() {
		log.Println("testing mode enabled — skipping external API price refresh")
		return
	}
	s.syncStockPrices(ctx)
	s.refreshForexRates()
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}

	// Invalidate all cached securities after price refresh
	if s.cache != nil {
		if err := s.cache.DeleteByPattern(ctx, "security:*"); err != nil {
			log.Printf("warn: failed to invalidate security cache: %v", err)
		}
	}

	StockPriceRefreshDuration.Observe(time.Since(start).Seconds())
	log.Println("price refresh complete")
}

// StartPeriodicRefresh launches a background goroutine that refreshes prices.
func (s *SecuritySyncService) StartPeriodicRefresh(ctx context.Context, intervalMins int) {
	if intervalMins <= 0 {
		intervalMins = 15
	}
	ticker := time.NewTicker(time.Duration(intervalMins) * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.RefreshPrices(ctx)
			case <-ctx.Done():
				log.Println("stopping periodic security price refresh")
				return
			}
		}
	}()
	log.Printf("periodic security price refresh started (every %d min)", intervalMins)
}

func (s *SecuritySyncService) isTestingMode() bool {
	val, err := s.settingRepo.Get("testing_mode")
	if err != nil {
		return false
	}
	return val == "true"
}

// --- Exchanges ---

// syncExchanges delegates to the active Source to fetch exchanges, then upserts
// each returned exchange into the DB via the exchange repository.
func (s *SecuritySyncService) syncExchanges() {
	src := s.Source()
	exchanges, err := src.FetchExchanges(context.Background())
	if err != nil {
		log.Printf("WARN: failed to fetch exchanges from %s: %v", src.Name(), err)
		return
	}
	for i := range exchanges {
		ex := exchanges[i]
		if err := s.exchangeRepo.UpsertByMICCode(&ex); err != nil {
			log.Printf("WARN: failed to upsert exchange %s: %v", ex.MICCode, err)
		}
	}
	log.Printf("synced %d exchanges from source %s", len(exchanges), src.Name())
}

// --- Stocks ---

// syncStocks delegates to the active Source to fetch stocks, then upserts each
// into the DB. Listing rows are created later by SeedAll via
// listingSvc.SyncListingsFromSecurities() — matching the pre-refactor behaviour.
func (s *SecuritySyncService) syncStocks(ctx context.Context) {
	src := s.Source()
	stocks, err := src.FetchStocks(ctx)
	if err != nil {
		log.Printf("WARN: failed to fetch stocks from %s: %v", src.Name(), err)
		return
	}
	for i := range stocks {
		sw := stocks[i]
		stock := sw.Stock
		if err := s.stockRepo.UpsertByTicker(&stock); err != nil {
			log.Printf("WARN: failed to upsert stock %s: %v", stock.Ticker, err)
		}
	}
	log.Printf("synced %d stocks from source %s", len(stocks), src.Name())
}

func (s *SecuritySyncService) syncStockPrices(ctx context.Context) {
	if s.avClient == nil {
		return
	}

	stocks, _, err := s.stockRepo.List(repository.StockFilter{Page: 1, PageSize: 1000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for price refresh: %v", err)
		return
	}

	for _, stock := range stocks {
		select {
		case <-ctx.Done():
			return
		default:
		}

		quote, err := s.avClient.FetchQuote(stock.Ticker)
		if err != nil {
			log.Printf("WARN: failed to refresh price for %s: %v", stock.Ticker, err)
			continue
		}

		stock.Price = quote.Price
		stock.High = quote.High
		stock.Low = quote.Low
		stock.Change = quote.Change
		stock.Volume = quote.Volume
		stock.LastRefresh = time.Now()

		if err := s.stockRepo.UpsertByTicker(&stock); err != nil {
			log.Printf("WARN: failed to update stock %s price: %v", stock.Ticker, err)
		}

		// Dual-write to InfluxDB for intraday candle data
		if listing, err := s.listingSvc.GetListingForSecurity(stock.ID, "stock"); err == nil {
			exchangeAcronym := ""
			if ex, err := s.exchangeRepo.GetByID(stock.ExchangeID); err == nil {
				exchangeAcronym = ex.Acronym
			}
			writeSecurityPricePoint(
				s.influxClient, listing.ID, "stock", stock.Ticker, exchangeAcronym,
				stock.Price, stock.High, stock.Low, stock.Change, stock.Volume,
				time.Now(),
			)
		}

		time.Sleep(12 * time.Second) // rate limit
	}
}

// --- Futures ---

// seedFutures delegates to the active Source to fetch futures contracts, then
// upserts each into the DB. The futuresSeedPath is kept as parameter for
// compatibility with SeedAll's call site; ExternalSource reads it internally.
func (s *SecuritySyncService) seedFutures(_ string) {
	src := s.Source()
	futures, err := src.FetchFutures(context.Background())
	if err != nil {
		log.Printf("WARN: failed to fetch futures from %s: %v", src.Name(), err)
		return
	}
	for i := range futures {
		fw := futures[i]
		fc := fw.Futures
		if err := s.futuresRepo.UpsertByTicker(&fc); err != nil {
			log.Printf("WARN: failed to upsert futures %s: %v", fc.Ticker, err)
		}
	}
	log.Printf("seeded %d futures contracts from source %s", len(futures), src.Name())
}

// --- Forex Pairs ---

// seedForexPairs delegates to the active Source to fetch forex pairs, then
// upserts each into the DB.
func (s *SecuritySyncService) seedForexPairs() {
	src := s.Source()
	pairs, err := src.FetchForex(context.Background())
	if err != nil {
		log.Printf("WARN: failed to fetch forex pairs from %s: %v", src.Name(), err)
		return
	}
	for i := range pairs {
		fp := pairs[i].Forex
		if err := s.forexRepo.UpsertByTicker(&fp); err != nil {
			log.Printf("WARN: failed to upsert forex pair %s: %v", fp.Ticker, err)
		}
	}
	log.Printf("seeded %d forex pairs from source %s", len(pairs), src.Name())
}

func (s *SecuritySyncService) refreshForexRates() {
	if s.finnhubClient == nil {
		return
	}
	for _, base := range SupportedCurrencies {
		rates, err := s.finnhubClient.FetchForexRates(base)
		if err != nil {
			log.Printf("WARN: failed to refresh forex rates for %s: %v", base, err)
			continue
		}
		for quote, rate := range rates {
			ticker := fmt.Sprintf("%s/%s", base, quote)
			existing, err := s.forexRepo.GetByTicker(ticker)
			if err != nil {
				continue
			}
			existing.ExchangeRate = decimal.NewFromFloat(rate)
			existing.LastRefresh = time.Now()
			if err := s.forexRepo.Update(existing); err != nil {
				log.Printf("WARN: failed to update forex rate %s: %v", ticker, err)
			}

			// Dual-write to InfluxDB
			if listing, err := s.listingSvc.GetListingForSecurity(existing.ID, "forex"); err == nil {
				writeSecurityPricePoint(
					s.influxClient, listing.ID, "forex", ticker, "FOREX",
					existing.ExchangeRate, existing.High, existing.Low, existing.Change, existing.Volume,
					time.Now(),
				)
			}
		}
	}
}

// --- Options ---

func (s *SecuritySyncService) generateAllOptions() {
	stocks, _, err := s.stockRepo.List(repository.StockFilter{Page: 1, PageSize: 1000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for option generation: %v", err)
		return
	}

	totalGenerated := 0
	for _, stock := range stocks {
		stock := stock

		// Resolve the stock's exchange via its listing row.
		stockListing, err := s.listingSvc.FindByStock(stock.ID)
		if err != nil {
			log.Printf("WARN: failed to look up listing for stock %s: %v; skipping option generation", stock.Ticker, err)
			continue
		}
		if stockListing == nil {
			log.Printf("WARN: no listing for stock %s; skipping option generation", stock.Ticker)
			continue
		}

		options := GenerateOptionsForStock(&stock)
		for i := range options {
			opt := &options[i]
			if err := s.optionRepo.UpsertByTicker(opt); err != nil {
				log.Printf("WARN: failed to upsert option %s: %v", opt.Ticker, err)
				continue
			}
			optListing := &model.Listing{
				SecurityID:   opt.ID,
				SecurityType: "option",
				ExchangeID:   stockListing.ExchangeID,
				Price:        opt.Premium,
				LastRefresh:  time.Now(),
			}
			savedListing, err := s.listingSvc.UpsertForOption(optListing)
			if err != nil {
				log.Printf("WARN: failed to upsert option listing for %s: %v", opt.Ticker, err)
				continue
			}
			if err := s.optionRepo.SetListingID(opt.ID, savedListing.ID); err != nil {
				log.Printf("WARN: failed to set listing_id on option %s: %v", opt.Ticker, err)
				continue
			}
		}
		totalGenerated += len(options)
	}

	// Clean up expired options
	deleted, err := s.optionRepo.DeleteExpiredBefore(time.Now())
	if err != nil {
		log.Printf("WARN: failed to clean expired options: %v", err)
	}

	log.Printf("generated %d options for %d stocks, cleaned %d expired", totalGenerated, len(stocks), deleted)
}

// GenerateAllOptionsForTest is a test-only exported wrapper for generateAllOptions.
func (s *SecuritySyncService) GenerateAllOptionsForTest() {
	s.generateAllOptions()
}

// --- Switch orchestration ---

// SwitchSource atomically switches the active data source. Wipes all
// stock-service tables and reseeds from the new source. Concurrent calls
// fail fast with an error.
func (s *SecuritySyncService) SwitchSource(ctx context.Context, newSource source.Source) error {
	if !s.switchMu.TryLock() {
		return fmt.Errorf("another source switch is in progress")
	}
	defer s.switchMu.Unlock()

	s.setStatus("reseeding", "")
	s.statusMu.Lock()
	s.startedAt = time.Now()
	s.statusMu.Unlock()

	// Stop the current simulator refresh loop if any.
	if s.refreshCtx != nil {
		s.refreshCtx()
		s.refreshCtx = nil
	}

	// Persist the new source choice so restarts pick it up.
	if err := s.settingRepo.Set("active_stock_source", newSource.Name()); err != nil {
		s.setStatus("failed", err.Error())
		return err
	}

	// Wipe.
	if s.wipe != nil {
		if err := s.wipe.WipeAll(); err != nil {
			s.setStatus("failed", "wipe: "+err.Error())
			return err
		}
	}

	// Swap the source pointer.
	s.SetSource(newSource)

	// Reseed synchronously for generated (local, instant); async for others.
	if newSource.Name() == "generated" {
		if err := s.reseedAll(ctx, ""); err != nil {
			s.setStatus("failed", err.Error())
			return err
		}
		s.setStatus("idle", "")
		return nil
	}

	// Async reseed for external / simulator sources.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.reseedAll(bgCtx, ""); err != nil {
			s.setStatus("failed", err.Error())
			return
		}
		s.setStatus("idle", "")
		if newSource.Name() == "simulator" {
			s.startSimulatorRefreshLoop()
		}
	}()
	return nil
}

// reseedAll calls SeedAll and wraps it so it returns an error.
// SeedAll itself logs errors internally and never returns one, so this
// always returns nil — but having the error signature lets us slot it into
// the orchestration flow uniformly.
func (s *SecuritySyncService) reseedAll(ctx context.Context, futuresSeedPath string) error {
	s.SeedAll(ctx, futuresSeedPath)
	return nil
}

func (s *SecuritySyncService) setStatus(status, errMsg string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status = status
	s.lastErr = errMsg
}

// GetStatus returns the current switch status, last error (if any),
// when the current switch was started, and the active source name.
func (s *SecuritySyncService) GetStatus() (status, lastErr string, startedAt time.Time, sourceName string) {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	status = s.status
	lastErr = s.lastErr
	startedAt = s.startedAt
	sourceName = s.Source().Name()
	return
}

// StartSimulatorRefreshLoopIfActive starts the simulator price refresh
// goroutine if the currently active source is the simulator. Safe to call
// from cmd/main.go on boot.
func (s *SecuritySyncService) StartSimulatorRefreshLoopIfActive() {
	if s.Source().Name() == "simulator" {
		s.startSimulatorRefreshLoop()
	}
}

// startSimulatorRefreshLoop launches a 3s ticker that re-fetches prices from
// the simulator and updates the DB. Runs until ctx is cancelled by the next
// SwitchSource call.
func (s *SecuritySyncService) startSimulatorRefreshLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	s.refreshCtx = cancel
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshSimulatorPrices(ctx)
			}
		}
	}()
}

// refreshSimulatorPrices re-fetches the current Source and updates the
// denormalized price columns on listings/stocks/futures/forex. Metadata
// rows are untouched. Called from the simulator refresh loop only.
func (s *SecuritySyncService) refreshSimulatorPrices(ctx context.Context) {
	src := s.Source()

	if stocks, err := src.FetchStocks(ctx); err == nil {
		for _, sw := range stocks {
			if err := s.listingSvc.UpdatePriceByTicker("stock", sw.Stock.Ticker, sw.Price, sw.High, sw.Low); err != nil {
				log.Printf("WARN: refresh listing stock %s: %v", sw.Stock.Ticker, err)
			}
			if err := s.stockRepo.UpdatePriceByTicker(sw.Stock.Ticker, sw.Price); err != nil {
				log.Printf("WARN: refresh stock %s: %v", sw.Stock.Ticker, err)
			}
		}
	} else {
		log.Printf("WARN: simulator refresh FetchStocks: %v", err)
	}

	if futures, err := src.FetchFutures(ctx); err == nil {
		for _, fw := range futures {
			if err := s.listingSvc.UpdatePriceByTicker("futures", fw.Futures.Ticker, fw.Price, fw.High, fw.Low); err != nil {
				log.Printf("WARN: refresh listing futures %s: %v", fw.Futures.Ticker, err)
			}
			if err := s.futuresRepo.UpdatePriceByTicker(fw.Futures.Ticker, fw.Price); err != nil {
				log.Printf("WARN: refresh futures %s: %v", fw.Futures.Ticker, err)
			}
		}
	} else {
		log.Printf("WARN: simulator refresh FetchFutures: %v", err)
	}

	if forex, err := src.FetchForex(ctx); err == nil {
		for _, fx := range forex {
			if err := s.listingSvc.UpdatePriceByTicker("forex", fx.Forex.Ticker, fx.Price, fx.High, fx.Low); err != nil {
				log.Printf("WARN: refresh listing forex %s: %v", fx.Forex.Ticker, err)
			}
			if err := s.forexRepo.UpdatePriceByTicker(fx.Forex.Ticker, fx.Price); err != nil {
				log.Printf("WARN: refresh forex %s: %v", fx.Forex.Ticker, err)
			}
		}
	} else {
		log.Printf("WARN: simulator refresh FetchForex: %v", err)
	}
}
