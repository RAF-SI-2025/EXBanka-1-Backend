package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Dedicated mocks for listing_service_test.go
//
// These mocks intentionally record every List()/UpsertBySecurity() call so
// the tests can assert that the listing sync pipeline covers every security
// passed to it — the bug we are guarding against is a silent pagination
// clamp that used to cap internal sync queries at 10 rows.
// ---------------------------------------------------------------------------

type listingSvcMockStockRepo struct {
	stocks        []model.Stock
	lastFilter    repository.StockFilter
	capturedSizes []int
}

func (m *listingSvcMockStockRepo) Create(s *model.Stock) error { return nil }
func (m *listingSvcMockStockRepo) GetByID(id uint64) (*model.Stock, error) {
	for i := range m.stocks {
		if m.stocks[i].ID == id {
			return &m.stocks[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockStockRepo) GetByTicker(ticker string) (*model.Stock, error) {
	for i := range m.stocks {
		if m.stocks[i].Ticker == ticker {
			return &m.stocks[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockStockRepo) Update(s *model.Stock) error         { return nil }
func (m *listingSvcMockStockRepo) UpsertByTicker(s *model.Stock) error { return nil }
func (m *listingSvcMockStockRepo) List(filter repository.StockFilter) ([]model.Stock, int64, error) {
	m.lastFilter = filter
	m.capturedSizes = append(m.capturedSizes, filter.PageSize)
	return m.stocks, int64(len(m.stocks)), nil
}
func (m *listingSvcMockStockRepo) UpdatePriceByTicker(ticker string, price decimal.Decimal) error {
	return nil
}

type listingSvcMockFuturesRepo struct {
	futures       []model.FuturesContract
	capturedSizes []int
}

func (m *listingSvcMockFuturesRepo) Create(f *model.FuturesContract) error { return nil }
func (m *listingSvcMockFuturesRepo) GetByID(id uint64) (*model.FuturesContract, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockFuturesRepo) GetByTicker(ticker string) (*model.FuturesContract, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockFuturesRepo) Update(f *model.FuturesContract) error         { return nil }
func (m *listingSvcMockFuturesRepo) UpsertByTicker(f *model.FuturesContract) error { return nil }
func (m *listingSvcMockFuturesRepo) List(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
	m.capturedSizes = append(m.capturedSizes, filter.PageSize)
	return m.futures, int64(len(m.futures)), nil
}
func (m *listingSvcMockFuturesRepo) UpdatePriceByTicker(ticker string, price decimal.Decimal) error {
	return nil
}

type listingSvcMockForexRepo struct {
	pairs         []model.ForexPair
	capturedSizes []int
}

func (m *listingSvcMockForexRepo) Create(fp *model.ForexPair) error { return nil }
func (m *listingSvcMockForexRepo) GetByID(id uint64) (*model.ForexPair, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockForexRepo) GetByTicker(ticker string) (*model.ForexPair, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockForexRepo) Update(fp *model.ForexPair) error         { return nil }
func (m *listingSvcMockForexRepo) UpsertByTicker(fp *model.ForexPair) error { return nil }
func (m *listingSvcMockForexRepo) List(filter repository.ForexFilter) ([]model.ForexPair, int64, error) {
	m.capturedSizes = append(m.capturedSizes, filter.PageSize)
	return m.pairs, int64(len(m.pairs)), nil
}
func (m *listingSvcMockForexRepo) UpdatePriceByTicker(ticker string, rate decimal.Decimal) error {
	return nil
}

// listingSvcMockListingRepo is a real in-memory store for Listing rows. Unlike
// the stub in security_sync_test.go (whose UpsertBySecurity is a no-op), this
// one actually persists upserts so we can count what sync produced.
type listingSvcMockListingRepo struct {
	listings     map[uint64]*model.Listing
	nextID       uint64
	upsertBySec  int
	upsertForOpt int
}

func newListingSvcMockListingRepo() *listingSvcMockListingRepo {
	return &listingSvcMockListingRepo{
		listings: make(map[uint64]*model.Listing),
		nextID:   1,
	}
}

func (m *listingSvcMockListingRepo) Create(listing *model.Listing) error {
	listing.ID = m.nextID
	m.nextID++
	cp := *listing
	m.listings[listing.ID] = &cp
	return nil
}
func (m *listingSvcMockListingRepo) GetByID(id uint64) (*model.Listing, error) {
	l, ok := m.listings[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *l
	return &cp, nil
}
func (m *listingSvcMockListingRepo) GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	for _, l := range m.listings {
		if l.SecurityID == securityID && l.SecurityType == securityType {
			cp := *l
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *listingSvcMockListingRepo) ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error) {
	idSet := make(map[uint64]struct{}, len(securityIDs))
	for _, id := range securityIDs {
		idSet[id] = struct{}{}
	}
	var out []model.Listing
	for _, l := range m.listings {
		if l.SecurityType != securityType {
			continue
		}
		if _, ok := idSet[l.SecurityID]; ok {
			out = append(out, *l)
		}
	}
	return out, nil
}
func (m *listingSvcMockListingRepo) Update(listing *model.Listing) error {
	if _, ok := m.listings[listing.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	cp := *listing
	m.listings[listing.ID] = &cp
	return nil
}
func (m *listingSvcMockListingRepo) UpsertBySecurity(listing *model.Listing) error {
	m.upsertBySec++
	for id, existing := range m.listings {
		if existing.SecurityID == listing.SecurityID && existing.SecurityType == listing.SecurityType {
			cp := *listing
			cp.ID = existing.ID
			m.listings[id] = &cp
			return nil
		}
	}
	listing.ID = m.nextID
	m.nextID++
	cp := *listing
	m.listings[listing.ID] = &cp
	return nil
}
func (m *listingSvcMockListingRepo) UpsertForOption(listing *model.Listing) (*model.Listing, error) {
	m.upsertForOpt++
	for id, existing := range m.listings {
		if existing.SecurityID == listing.SecurityID && existing.SecurityType == listing.SecurityType {
			cp := *listing
			cp.ID = existing.ID
			m.listings[id] = &cp
			out := cp
			return &out, nil
		}
	}
	listing.ID = m.nextID
	m.nextID++
	cp := *listing
	m.listings[listing.ID] = &cp
	out := cp
	return &out, nil
}
func (m *listingSvcMockListingRepo) ListAll() ([]model.Listing, error) {
	out := make([]model.Listing, 0, len(m.listings))
	for _, l := range m.listings {
		out = append(out, *l)
	}
	return out, nil
}
func (m *listingSvcMockListingRepo) ListBySecurityType(securityType string) ([]model.Listing, error) {
	var out []model.Listing
	for _, l := range m.listings {
		if l.SecurityType == securityType {
			out = append(out, *l)
		}
	}
	return out, nil
}
func (m *listingSvcMockListingRepo) UpdatePriceByTicker(securityType, ticker string, price, high, low decimal.Decimal) error {
	return nil
}

// countByType returns how many stored listings have the given security_type.
func (m *listingSvcMockListingRepo) countByType(securityType string) int {
	n := 0
	for _, l := range m.listings {
		if l.SecurityType == securityType {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestListingService_syncFuturesListings_AllTwenty asserts the full set of
// futures contracts receives a corresponding listing row. Regression test for
// the pagination-clamp bug where PageSize > 100 was silently reset to 10,
// dropping the latter half of futures rows.
func TestListingService_syncFuturesListings_AllTwenty(t *testing.T) {
	futures := make([]model.FuturesContract, 20)
	for i := 0; i < 20; i++ {
		futures[i] = model.FuturesContract{
			ID:         uint64(i + 1),
			Ticker:     "F" + string(rune('A'+i)),
			Name:       "Futures " + string(rune('A'+i)),
			ExchangeID: uint64((i % 5) + 1),
			Price:      decimal.NewFromInt(int64(100 + i)),
		}
	}
	futuresRepo := &listingSvcMockFuturesRepo{futures: futures}
	listingRepo := newListingSvcMockListingRepo()

	// The other two repos must be non-nil because SyncListingsFromSecurities
	// iterates all three types; we supply empty stubs so the other syncs are
	// no-ops.
	stockRepo := &listingSvcMockStockRepo{}
	forexRepo := &listingSvcMockForexRepo{}
	svc := NewListingService(listingRepo, nil, stockRepo, futuresRepo, forexRepo)
	svc.SyncListingsFromSecurities()

	require.Equal(t, 20, listingRepo.countByType("futures"),
		"expected a listing row for every one of the 20 futures contracts")
	require.Equal(t, 20, listingRepo.upsertBySec,
		"UpsertBySecurity should have been invoked once per futures contract")

	// Defence-in-depth: the sync must ask for a page size that exceeds 10 so
	// the previous pagination-clamp bug (pageSize>100 reset to 10) cannot
	// silently recur. A page size <= 10 would regress to the original bug.
	require.NotEmpty(t, futuresRepo.capturedSizes, "List must have been called")
	for _, sz := range futuresRepo.capturedSizes {
		require.Greater(t, sz, 10,
			"sync must request a page size > 10 (got %d) to avoid the legacy clamp", sz)
	}
}

// TestListingService_SyncListingsFromSecurities_AllThreeTypes covers stocks,
// futures, and forex in one pass, asserting that every supplied security gets
// a listing row. Exercises the entire SyncListingsFromSecurities entry point.
func TestListingService_SyncListingsFromSecurities_AllThreeTypes(t *testing.T) {
	stocks := make([]model.Stock, 25)
	for i := 0; i < 25; i++ {
		stocks[i] = model.Stock{
			ID:         uint64(i + 1),
			Ticker:     "S" + string(rune('A'+i%26)),
			Name:       "Stock",
			ExchangeID: uint64((i % 5) + 1),
			Price:      decimal.NewFromInt(int64(50 + i)),
		}
	}
	futures := make([]model.FuturesContract, 20)
	for i := 0; i < 20; i++ {
		futures[i] = model.FuturesContract{
			ID:         uint64(i + 1),
			Ticker:     "F" + string(rune('A'+i)),
			ExchangeID: uint64((i % 5) + 1),
			Price:      decimal.NewFromInt(int64(100 + i)),
		}
	}
	pairs := make([]model.ForexPair, 56)
	for i := 0; i < 56; i++ {
		pairs[i] = model.ForexPair{
			ID:           uint64(i + 1),
			Ticker:       "P",
			ExchangeID:   uint64((i % 5) + 1),
			ExchangeRate: decimal.NewFromInt(int64(1 + i)),
		}
	}

	stockRepo := &listingSvcMockStockRepo{stocks: stocks}
	futuresRepo := &listingSvcMockFuturesRepo{futures: futures}
	forexRepo := &listingSvcMockForexRepo{pairs: pairs}
	listingRepo := newListingSvcMockListingRepo()

	svc := NewListingService(listingRepo, nil, stockRepo, futuresRepo, forexRepo)
	svc.SyncListingsFromSecurities()

	require.Equal(t, 25, listingRepo.countByType("stock"))
	require.Equal(t, 20, listingRepo.countByType("futures"))
	require.Equal(t, 56, listingRepo.countByType("forex"))
}
