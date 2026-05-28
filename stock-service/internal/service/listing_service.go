package service

import (
	"errors"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

type ListingService struct {
	listingRepo ListingRepo
	dailyRepo   DailyPriceRepo
	stockRepo   StockRepo
	futuresRepo FuturesRepo
	forexRepo   ForexPairRepo
}

func NewListingService(
	listingRepo ListingRepo,
	dailyRepo DailyPriceRepo,
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
) *ListingService {
	return &ListingService{
		listingRepo: listingRepo,
		dailyRepo:   dailyRepo,
		stockRepo:   stockRepo,
		futuresRepo: futuresRepo,
		forexRepo:   forexRepo,
	}
}

// SyncListingsFromSecurities creates or updates listings for all securities.
// Called after security seed/sync completes.
func (s *ListingService) SyncListingsFromSecurities() {
	s.syncStockListings()
	s.syncFuturesListings()
	s.syncForexListings()
}

func (s *ListingService) syncStockListings() {
	stocks, _, err := s.stockRepo.List(repository.StockFilter{Page: 1, PageSize: 10000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for listing sync: %v", err)
		return
	}
	count := 0
	for _, stock := range stocks {
		listing := &model.Listing{
			SecurityID:   stock.ID,
			SecurityType: "stock",
			ExchangeID:   stock.ExchangeID,
			Price:        stock.Price,
			High:         stock.High,
			Low:          stock.Low,
			Change:       stock.Change,
			Volume:       stock.Volume,
			LastRefresh:  stock.LastRefresh,
		}
		if err := s.listingRepo.UpsertBySecurity(listing); err != nil {
			log.Printf("WARN: failed to upsert listing for stock %s: %v", stock.Ticker, err)
			continue
		}
		count++
	}
	log.Printf("synced %d stock listings", count)
}

func (s *ListingService) syncFuturesListings() {
	futures, _, err := s.futuresRepo.List(repository.FuturesFilter{Page: 1, PageSize: 10000})
	if err != nil {
		log.Printf("WARN: failed to list futures for listing sync: %v", err)
		return
	}
	count := 0
	for _, f := range futures {
		listing := &model.Listing{
			SecurityID:   f.ID,
			SecurityType: "futures",
			ExchangeID:   f.ExchangeID,
			Price:        f.Price,
			High:         f.High,
			Low:          f.Low,
			Change:       f.Change,
			Volume:       f.Volume,
			LastRefresh:  f.LastRefresh,
		}
		if err := s.listingRepo.UpsertBySecurity(listing); err != nil {
			log.Printf("WARN: failed to upsert listing for futures %s: %v", f.Ticker, err)
			continue
		}
		count++
	}
	log.Printf("synced %d futures listings", count)
}

func (s *ListingService) syncForexListings() {
	pairs, _, err := s.forexRepo.List(repository.ForexFilter{Page: 1, PageSize: 10000})
	if err != nil {
		log.Printf("WARN: failed to list forex pairs for listing sync: %v", err)
		return
	}
	count := 0
	for _, fp := range pairs {
		listing := &model.Listing{
			SecurityID:   fp.ID,
			SecurityType: "forex",
			ExchangeID:   fp.ExchangeID,
			Price:        fp.ExchangeRate,
			High:         fp.High,
			Low:          fp.Low,
			Change:       fp.Change,
			Volume:       fp.Volume,
			LastRefresh:  fp.LastRefresh,
		}
		if err := s.listingRepo.UpsertBySecurity(listing); err != nil {
			log.Printf("WARN: failed to upsert listing for forex %s: %v", fp.Ticker, err)
			continue
		}
		count++
	}
	log.Printf("synced %d forex listings", count)
}

// FindByStock returns the "stock" listing for the given stock ID.
func (s *ListingService) FindByStock(stockID uint64) (*model.Listing, error) {
	listing, err := s.listingRepo.GetBySecurityIDAndType(stockID, "stock")
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return listing, nil
}

// UpsertForOption upserts a listing with security_type="option" and returns the persisted listing (with ID).
func (s *ListingService) UpsertForOption(listing *model.Listing) (*model.Listing, error) {
	return s.listingRepo.UpsertForOption(listing)
}

// GetListing retrieves a listing by ID.
func (s *ListingService) GetListing(id uint64) (*model.Listing, error) {
	listing, err := s.listingRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("listing not found")
		}
		return nil, err
	}
	return listing, nil
}

// GetListingForSecurity finds the listing for a given security.
func (s *ListingService) GetListingForSecurity(securityID uint64, securityType string) (*model.Listing, error) {
	listing, err := s.listingRepo.GetBySecurityIDAndType(securityID, securityType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("listing not found for security")
		}
		return nil, err
	}
	return listing, nil
}

// SnapshotIntradayPrices writes one ListingDailyPriceInfo row per listing
// at the current timestamp. Called after every successful price refresh so
// the /history endpoint surfaces oscillation/intraday movement, not just
// the end-of-day cron snapshot. The unique-by-timestamp key means each tick
// inserts a fresh row.
func (s *ListingService) SnapshotIntradayPrices() {
	if s.listingRepo == nil || s.dailyRepo == nil {
		return
	}
	listings, err := s.listingRepo.ListAll()
	if err != nil {
		log.Printf("WARN: intraday snapshot: failed to list listings: %v", err)
		return
	}
	now := time.Now()
	count := 0
	for _, l := range listings {
		info := &model.ListingDailyPriceInfo{
			ListingID: l.ID,
			Date:      now,
			Price:     l.Price,
			High:      l.Price, // per-snapshot is a single point-in-time
			Low:       l.Price,
			Change:    decimal.Decimal{}, // no per-snapshot delta; bucketer derives close-open
			Volume:    l.Volume,
		}
		if err := s.dailyRepo.UpsertByListingAndDate(info); err != nil {
			log.Printf("WARN: intraday snapshot: listing %d: %v", l.ID, err)
			continue
		}
		count++
	}
	log.Printf("intraday snapshot: recorded %d listings at %s", count, now.Format(time.RFC3339))
}

// GetPriceHistory retrieves OHLC-bucketed price history for a listing. The
// bucket interval is derived from the period so each timeframe produces a
// sensible number of candles regardless of how dense the underlying snapshot
// stream is. Pagination is bypassed when bucketing — the bucket interval
// itself caps the row count to ~300-400 per period.
func (s *ListingService) GetPriceHistory(listingID uint64, period string, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	from, to := periodToDateRange(period)
	bucket := periodToBucketSeconds(period)
	rows, err := s.dailyRepo.GetHistoryBucketed(listingID, from, to, bucket)
	if err != nil {
		return nil, 0, err
	}
	return rows, int64(len(rows)), nil
}

// periodToBucketSeconds maps a chart period to the candle bucket width that
// produces a sensible candle count (a few hundred max). Long periods bucket
// coarsely so e.g. a year doesn't render 525k 1-minute rows; short periods
// keep fine granularity.
func periodToBucketSeconds(period string) int {
	switch period {
	case "day":
		return 5 * 60 // 5 minutes
	case "week":
		return 30 * 60 // 30 minutes
	case "month":
		return 2 * 3600 // 2 hours
	case "year":
		return 24 * 3600 // 1 day
	case "5y", "all":
		return 7 * 24 * 3600 // 1 week
	default:
		return 2 * 3600
	}
}

// GetPriceHistoryForSecurity looks up the listing for a security, then gets history.
func (s *ListingService) GetPriceHistoryForSecurity(securityID uint64, securityType, period string, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	listing, err := s.listingRepo.GetBySecurityIDAndType(securityID, securityType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, errors.New("listing not found for security")
		}
		return nil, 0, err
	}
	return s.GetPriceHistory(listing.ID, period, page, pageSize)
}

// GetDerivedData computes derived listing data by looking up the underlying security.
func (s *ListingService) GetDerivedData(listing *model.Listing) DerivedListingData {
	var contractSizeOverride int64
	var outstandingShares int64

	switch listing.SecurityType {
	case "futures":
		if f, err := s.futuresRepo.GetByID(listing.SecurityID); err == nil {
			contractSizeOverride = f.ContractSize
		}
	case "stock":
		if st, err := s.stockRepo.GetByID(listing.SecurityID); err == nil {
			outstandingShares = st.OutstandingShares
		}
	}

	return CalculateDerivedData(
		listing.SecurityType,
		listing.Price,
		listing.Change,
		listing.Volume,
		contractSizeOverride,
		outstandingShares,
		decimal.Decimal{}, // stockPrice only relevant for options
	)
}

// UpdatePriceByTicker delegates price-only updates for simulator refresh loops.
func (s *ListingService) UpdatePriceByTicker(securityType, ticker string, price, high, low decimal.Decimal) error {
	return s.listingRepo.UpdatePriceByTicker(securityType, ticker, price, high, low)
}

// periodToDateRange converts a period string to (from, to) dates.
func periodToDateRange(period string) (time.Time, time.Time) {
	now := time.Now()
	to := now

	switch period {
	case "day":
		return now.AddDate(0, 0, -1), to
	case "week":
		return now.AddDate(0, 0, -7), to
	case "month":
		return now.AddDate(0, -1, 0), to
	case "year":
		return now.AddDate(-1, 0, 0), to
	case "5y":
		return now.AddDate(-5, 0, 0), to
	case "all":
		return time.Time{}, to // zero time = no lower bound
	default:
		return now.AddDate(0, -1, 0), to // default: month
	}
}
