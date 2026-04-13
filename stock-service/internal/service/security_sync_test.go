package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Minimal mocks for SecuritySyncService
// ---------------------------------------------------------------------------

// syncMockStockRepo holds one stock and satisfies StockRepo.
type syncMockStockRepo struct {
	stocks []model.Stock
}

func (m *syncMockStockRepo) Create(s *model.Stock) error { return nil }
func (m *syncMockStockRepo) GetByID(id uint64) (*model.Stock, error) {
	for i := range m.stocks {
		if m.stocks[i].ID == id {
			return &m.stocks[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *syncMockStockRepo) GetByTicker(ticker string) (*model.Stock, error) {
	for i := range m.stocks {
		if m.stocks[i].Ticker == ticker {
			return &m.stocks[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *syncMockStockRepo) Update(s *model.Stock) error { return nil }
func (m *syncMockStockRepo) UpsertByTicker(s *model.Stock) error {
	if s.ID == 0 {
		s.ID = 1
	}
	for i := range m.stocks {
		if m.stocks[i].Ticker == s.Ticker {
			m.stocks[i] = *s
			return nil
		}
	}
	m.stocks = append(m.stocks, *s)
	return nil
}
func (m *syncMockStockRepo) List(filter repository.StockFilter) ([]model.Stock, int64, error) {
	return m.stocks, int64(len(m.stocks)), nil
}

// syncMockOptionRepo stores upserted options in memory and tracks SetListingID calls.
type syncMockOptionRepo struct {
	options  map[string]*model.Option
	nextID   uint64
	listingIDs map[uint64]uint64 // optionID -> listingID
}

func newSyncMockOptionRepo() *syncMockOptionRepo {
	return &syncMockOptionRepo{
		options:    make(map[string]*model.Option),
		nextID:     1,
		listingIDs: make(map[uint64]uint64),
	}
}

func (m *syncMockOptionRepo) Create(o *model.Option) error {
	o.ID = m.nextID
	m.nextID++
	cp := *o
	m.options[o.Ticker] = &cp
	return nil
}
func (m *syncMockOptionRepo) GetByID(id uint64) (*model.Option, error) {
	for _, o := range m.options {
		if o.ID == id {
			cp := *o
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *syncMockOptionRepo) GetByTicker(ticker string) (*model.Option, error) {
	o, ok := m.options[ticker]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *o
	return &cp, nil
}
func (m *syncMockOptionRepo) Update(o *model.Option) error { return nil }
func (m *syncMockOptionRepo) UpsertByTicker(o *model.Option) error {
	if existing, ok := m.options[o.Ticker]; ok {
		o.ID = existing.ID
		cp := *o
		m.options[o.Ticker] = &cp
		return nil
	}
	o.ID = m.nextID
	m.nextID++
	cp := *o
	m.options[o.Ticker] = &cp
	return nil
}
func (m *syncMockOptionRepo) List(filter repository.OptionFilter) ([]model.Option, int64, error) {
	var result []model.Option
	for _, o := range m.options {
		result = append(result, *o)
	}
	return result, int64(len(result)), nil
}
func (m *syncMockOptionRepo) DeleteExpiredBefore(cutoff time.Time) (int64, error) { return 0, nil }
func (m *syncMockOptionRepo) SetListingID(optionID, listingID uint64) error {
	m.listingIDs[optionID] = listingID
	// Also update the stored option so tests can inspect it.
	for ticker, o := range m.options {
		if o.ID == optionID {
			cp := *o
			cp.ListingID = &listingID
			m.options[ticker] = &cp
			return nil
		}
	}
	return nil
}

// syncMockSettingRepo always reports testing_mode=false (generateAllOptions doesn't check it).
type syncMockSettingRepo struct{}

func (m *syncMockSettingRepo) Get(key string) (string, error) {
	return "", gorm.ErrRecordNotFound
}
func (m *syncMockSettingRepo) Set(key, value string) error { return nil }

// syncMockListingRepo stores listings in memory and satisfies ListingRepo.
type syncMockListingRepo struct {
	listings map[uint64]*model.Listing
	nextID   uint64
}

func newSyncMockListingRepo() *syncMockListingRepo {
	return &syncMockListingRepo{
		listings: make(map[uint64]*model.Listing),
		nextID:   1,
	}
}

func (m *syncMockListingRepo) addListing(l *model.Listing) {
	if l.ID == 0 {
		l.ID = m.nextID
		m.nextID++
	}
	cp := *l
	m.listings[l.ID] = &cp
}

func (m *syncMockListingRepo) Create(listing *model.Listing) error {
	listing.ID = m.nextID
	m.nextID++
	cp := *listing
	m.listings[listing.ID] = &cp
	return nil
}
func (m *syncMockListingRepo) GetByID(id uint64) (*model.Listing, error) {
	l, ok := m.listings[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *l
	return &cp, nil
}
func (m *syncMockListingRepo) GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	for _, l := range m.listings {
		if l.SecurityID == securityID && l.SecurityType == securityType {
			cp := *l
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *syncMockListingRepo) Update(listing *model.Listing) error {
	if _, ok := m.listings[listing.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	cp := *listing
	m.listings[listing.ID] = &cp
	return nil
}
func (m *syncMockListingRepo) UpsertBySecurity(l *model.Listing) error { return nil }
func (m *syncMockListingRepo) UpsertForOption(l *model.Listing) (*model.Listing, error) {
	// Find existing by (security_id, security_type).
	for id, existing := range m.listings {
		if existing.SecurityID == l.SecurityID && existing.SecurityType == l.SecurityType {
			existing.ExchangeID = l.ExchangeID
			existing.Price = l.Price
			existing.LastRefresh = l.LastRefresh
			m.listings[id] = existing
			cp := *existing
			return &cp, nil
		}
	}
	// Create new.
	l.ID = m.nextID
	m.nextID++
	cp := *l
	m.listings[l.ID] = &cp
	return &cp, nil
}
func (m *syncMockListingRepo) ListAll() ([]model.Listing, error) {
	var result []model.Listing
	for _, l := range m.listings {
		result = append(result, *l)
	}
	return result, nil
}
func (m *syncMockListingRepo) ListBySecurityType(securityType string) ([]model.Listing, error) {
	var result []model.Listing
	for _, l := range m.listings {
		if l.SecurityType == securityType {
			result = append(result, *l)
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Test
// ---------------------------------------------------------------------------

func TestGenerateAllOptions_AttachesListingToEveryOption(t *testing.T) {
	// Seed one stock with a price so GenerateOptionsForStock produces options.
	stock := model.Stock{
		ID:     1,
		Ticker: "AAPL",
		Name:   "Apple Inc.",
		Price:  decimal.NewFromInt(180),
	}
	stockRepo := &syncMockStockRepo{stocks: []model.Stock{stock}}

	optionRepo := newSyncMockOptionRepo()

	// Seed the stock listing so FindByStock returns it.
	listingRepo := newSyncMockListingRepo()
	stockListing := &model.Listing{
		ID:           1,
		SecurityID:   stock.ID,
		SecurityType: "stock",
		ExchangeID:   42,
		Price:        stock.Price,
	}
	listingRepo.addListing(stockListing)

	// nextID should start after the stock listing.
	listingRepo.nextID = 2

	listingSvc := NewListingService(listingRepo, nil, stockRepo, nil, nil)

	syncSvc := NewSecuritySyncService(
		stockRepo,
		nil, // futuresRepo — not used by generateAllOptions
		nil, // forexRepo — not used by generateAllOptions
		optionRepo,
		nil, // exchangeRepo — not used by generateAllOptions
		&syncMockSettingRepo{},
		nil, // avClient
		nil, // eodhClient
		nil, // alpacaClient
		nil, // finnhubClient
		listingSvc,
		"",  // csvPath
		nil, // redisCache
		nil, // influxClient
	)

	// Act: generate options (test-exported wrapper).
	syncSvc.GenerateAllOptionsForTest()

	// Assert: every option must have a non-nil ListingID pointing at an "option"
	// listing on the same exchange as the underlying stock.
	require.NotEmpty(t, optionRepo.options, "no options were generated")

	for ticker, opt := range optionRepo.options {
		require.NotNil(t, opt.ListingID, "option %s is missing listing_id", ticker)

		lid := *opt.ListingID
		listing, ok := listingRepo.listings[lid]
		require.True(t, ok, "option %s points to listing_id %d which does not exist", ticker, lid)
		require.Equal(t, "option", listing.SecurityType,
			"listing for option %s has wrong security_type", ticker)
		require.Equal(t, opt.ID, listing.SecurityID,
			"listing for option %s points to wrong security_id", ticker)
		require.Equal(t, uint64(42), listing.ExchangeID,
			"listing for option %s has wrong exchange_id", ticker)
	}
}
