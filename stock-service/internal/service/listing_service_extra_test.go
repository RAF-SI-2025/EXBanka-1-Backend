package service

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// listingSvcDailyMock is a minimal DailyPriceRepo capturing the args
// GetHistory was called with so periodToDateRange can be asserted.
type listingSvcDailyMock struct {
	gotListingID         uint64
	gotFrom, gotTo       time.Time
	gotPage, gotPageSize int
	rows                 []model.ListingDailyPriceInfo
	err                  error
}

func (m *listingSvcDailyMock) Create(_ *model.ListingDailyPriceInfo) error { return nil }
func (m *listingSvcDailyMock) UpsertByListingAndDate(_ *model.ListingDailyPriceInfo) error {
	return nil
}
func (m *listingSvcDailyMock) UpsertManyByListingAndDate(_ []model.ListingDailyPriceInfo) error {
	return nil
}
func (m *listingSvcDailyMock) GetHistory(listingID uint64, from, to time.Time, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	m.gotListingID = listingID
	m.gotFrom = from
	m.gotTo = to
	m.gotPage = page
	m.gotPageSize = pageSize
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.rows, int64(len(m.rows)), nil
}
func (m *listingSvcDailyMock) GetHistoryBucketed(listingID uint64, from, to time.Time, _ int) ([]model.ListingDailyPriceInfo, error) {
	m.gotListingID = listingID
	m.gotFrom = from
	m.gotTo = to
	if m.err != nil {
		return nil, m.err
	}
	return m.rows, nil
}

func TestListingService_FindByStock_Found(t *testing.T) {
	listingRepo := newListingSvcMockListingRepo()
	listingRepo.listings[1] = &model.Listing{ID: 1, SecurityID: 99, SecurityType: "stock"}
	svc := NewListingService(listingRepo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	got, err := svc.FindByStock(99)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 1 {
		t.Errorf("want listing id=1, got %+v", got)
	}
}

func TestListingService_FindByStock_NotFoundReturnsNil(t *testing.T) {
	listingRepo := newListingSvcMockListingRepo()
	svc := NewListingService(listingRepo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	got, err := svc.FindByStock(123)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for not-found, got %+v", got)
	}
}

func TestListingService_GetListing_Found(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	repo.listings[42] = &model.Listing{ID: 42, SecurityID: 9}
	svc := NewListingService(repo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	got, err := svc.GetListing(42)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != 42 {
		t.Errorf("got id %d", got.ID)
	}
}

func TestListingService_GetListing_NotFound(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	svc := NewListingService(repo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	_, err := svc.GetListing(404)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected wrapped error, not raw gorm: %v", err)
	}
}

func TestListingService_GetListingForSecurity_Found(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	repo.listings[1] = &model.Listing{ID: 1, SecurityID: 7, SecurityType: "stock"}
	svc := NewListingService(repo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	got, err := svc.GetListingForSecurity(7, "stock")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestListingService_GetListingForSecurity_NotFound(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	svc := NewListingService(repo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	_, err := svc.GetListingForSecurity(7, "stock")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListingService_GetPriceHistory_PeriodMonth(t *testing.T) {
	dailyRepo := &listingSvcDailyMock{rows: []model.ListingDailyPriceInfo{{ID: 1}}}
	svc := NewListingService(newListingSvcMockListingRepo(), dailyRepo, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	rows, total, err := svc.GetPriceHistory(99, "month", 1, 50)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("expected 1 row, got %d/%d", total, len(rows))
	}
	// GetPriceHistory now bucket-aggregates server-side, so pagination is
	// not forwarded; we only assert the listing id and the period window.
	if dailyRepo.gotListingID != 99 {
		t.Errorf("daily repo received wrong listing id: %+v", dailyRepo)
	}
	// month period: from should be roughly 30 days before now
	if dailyRepo.gotFrom.IsZero() {
		t.Error("from should not be zero for 'month'")
	}
}

func TestListingService_GetPriceHistory_PeriodAll(t *testing.T) {
	dailyRepo := &listingSvcDailyMock{}
	svc := NewListingService(newListingSvcMockListingRepo(), dailyRepo, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	_, _, err := svc.GetPriceHistory(1, "all", 1, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dailyRepo.gotFrom.IsZero() {
		t.Errorf("'all' period should pass zero from, got %v", dailyRepo.gotFrom)
	}
}

func TestListingService_GetPriceHistory_PeriodVariants(t *testing.T) {
	for _, period := range []string{"day", "week", "year", "5y", "weird"} {
		dailyRepo := &listingSvcDailyMock{}
		svc := NewListingService(newListingSvcMockListingRepo(), dailyRepo, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
		_, _, err := svc.GetPriceHistory(1, period, 1, 10)
		if err != nil {
			t.Fatalf("period %s: %v", period, err)
		}
		if dailyRepo.gotFrom.IsZero() {
			t.Errorf("period %s: expected non-zero from", period)
		}
	}
}

func TestListingService_GetPriceHistoryForSecurity_Found(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	repo.listings[1] = &model.Listing{ID: 1, SecurityID: 7, SecurityType: "stock"}
	dailyRepo := &listingSvcDailyMock{rows: []model.ListingDailyPriceInfo{{ID: 9}}}
	svc := NewListingService(repo, dailyRepo, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	rows, total, err := svc.GetPriceHistoryForSecurity(7, "stock", "month", 1, 25)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got %d/%d", total, len(rows))
	}
	if dailyRepo.gotListingID != 1 {
		t.Errorf("expected listing id 1, got %d", dailyRepo.gotListingID)
	}
}

func TestListingService_GetPriceHistoryForSecurity_NoListing(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	dailyRepo := &listingSvcDailyMock{}
	svc := NewListingService(repo, dailyRepo, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	_, _, err := svc.GetPriceHistoryForSecurity(7, "stock", "month", 1, 25)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListingService_GetDerivedData_Stock(t *testing.T) {
	stockRepo := &listingSvcMockStockRepo{stocks: []model.Stock{{ID: 1, OutstandingShares: 1000}}}
	svc := NewListingService(newListingSvcMockListingRepo(), nil, stockRepo, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})

	listing := &model.Listing{SecurityID: 1, SecurityType: "stock", Price: decimal.NewFromInt(50)}
	d := svc.GetDerivedData(listing)
	// MarketCap = price * outstanding = 50 * 1000 = 50000
	if !d.MarketCap.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("market cap: got %s want 50000", d.MarketCap)
	}
}

func TestListingService_GetDerivedData_Futures(t *testing.T) {
	futuresRepo := newFuturesRepoWithOne(7, 100)
	svc := NewListingService(newListingSvcMockListingRepo(), nil, &listingSvcMockStockRepo{}, futuresRepo, &listingSvcMockForexRepo{})
	listing := &model.Listing{SecurityID: 7, SecurityType: "futures", Price: decimal.NewFromInt(20)}
	_ = svc.GetDerivedData(listing)
	// Just ensure no panic — derived helpers covered elsewhere.
}

func TestListingService_GetDerivedData_Forex(t *testing.T) {
	svc := NewListingService(newListingSvcMockListingRepo(), nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	listing := &model.Listing{SecurityID: 1, SecurityType: "forex", Price: decimal.NewFromFloat(1.5)}
	_ = svc.GetDerivedData(listing)
}

func TestListingService_UpdatePriceByTicker_Delegates(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	svc := NewListingService(repo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	if err := svc.UpdatePriceByTicker("stock", "AAPL", decimal.NewFromInt(150), decimal.NewFromInt(151), decimal.NewFromInt(149)); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestListingService_UpsertForOption_Delegates(t *testing.T) {
	repo := newListingSvcMockListingRepo()
	svc := NewListingService(repo, nil, &listingSvcMockStockRepo{}, &listingSvcMockFuturesRepo{}, &listingSvcMockForexRepo{})
	got, err := svc.UpsertForOption(&model.Listing{SecurityID: 1, SecurityType: "option"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID == 0 {
		t.Errorf("expected persisted listing, got %+v", got)
	}
	if repo.upsertForOpt != 1 {
		t.Errorf("expected 1 upsert call, got %d", repo.upsertForOpt)
	}
}

func newFuturesRepoWithOne(id uint64, contractSize int64) *futuresRepoWithOne {
	return &futuresRepoWithOne{id: id, contractSize: contractSize}
}

type futuresRepoWithOne struct {
	id           uint64
	contractSize int64
}

func (m *futuresRepoWithOne) Create(_ *model.FuturesContract) error { return nil }
func (m *futuresRepoWithOne) GetByID(id uint64) (*model.FuturesContract, error) {
	if id == m.id {
		return &model.FuturesContract{ID: id, ContractSize: m.contractSize}, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *futuresRepoWithOne) GetByTicker(_ string) (*model.FuturesContract, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *futuresRepoWithOne) Update(_ *model.FuturesContract) error         { return nil }
func (m *futuresRepoWithOne) UpsertByTicker(_ *model.FuturesContract) error { return nil }
func (m *futuresRepoWithOne) List(_ repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
	return nil, 0, nil
}
func (m *futuresRepoWithOne) UpdatePriceByTicker(_ string, _ decimal.Decimal) error {
	return nil
}
