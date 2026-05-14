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

// secSvcStockRepo: minimal StockRepo for SecurityService tests.
type secSvcStockRepo struct {
	stocks  []model.Stock
	listErr error
	getErr  error
}

func (m *secSvcStockRepo) Create(_ *model.Stock) error { return nil }
func (m *secSvcStockRepo) GetByID(id uint64) (*model.Stock, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.stocks {
		if m.stocks[i].ID == id {
			return &m.stocks[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcStockRepo) GetByTicker(ticker string) (*model.Stock, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.stocks {
		if m.stocks[i].Ticker == ticker {
			return &m.stocks[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func TestSecurityService_GetByTicker_Found(t *testing.T) {
	stockRepo := &secSvcStockRepo{stocks: []model.Stock{{ID: 7, Ticker: "AAPL"}}}
	svc := newSecSvc(t, stockRepo, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	got, err := svc.GetByTicker("AAPL")
	if err != nil {
		t.Fatalf("GetByTicker: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("got id %d, want 7", got.ID)
	}
}
func (m *secSvcStockRepo) Update(_ *model.Stock) error         { return nil }
func (m *secSvcStockRepo) UpsertByTicker(_ *model.Stock) error { return nil }
func (m *secSvcStockRepo) List(_ repository.StockFilter) ([]model.Stock, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.stocks, int64(len(m.stocks)), nil
}
func (m *secSvcStockRepo) UpdatePriceByTicker(_ string, _ decimal.Decimal) error { return nil }

type secSvcOptionRepo struct {
	options []model.Option
	listErr error
	getErr  error
}

func (m *secSvcOptionRepo) Create(_ *model.Option) error { return nil }
func (m *secSvcOptionRepo) GetByID(id uint64) (*model.Option, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.options {
		if m.options[i].ID == id {
			return &m.options[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcOptionRepo) GetByTicker(_ string) (*model.Option, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcOptionRepo) Update(_ *model.Option) error         { return nil }
func (m *secSvcOptionRepo) UpsertByTicker(_ *model.Option) error { return nil }
func (m *secSvcOptionRepo) List(_ repository.OptionFilter) ([]model.Option, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.options, int64(len(m.options)), nil
}
func (m *secSvcOptionRepo) DeleteExpiredBefore(_ time.Time) (int64, error) { return 0, nil }
func (m *secSvcOptionRepo) SetListingID(_, _ uint64) error                 { return nil }

type secSvcFuturesRepo struct {
	futures []model.FuturesContract
	getErr  error
}

func (m *secSvcFuturesRepo) Create(_ *model.FuturesContract) error { return nil }
func (m *secSvcFuturesRepo) GetByID(id uint64) (*model.FuturesContract, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.futures {
		if m.futures[i].ID == id {
			return &m.futures[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcFuturesRepo) GetByTicker(_ string) (*model.FuturesContract, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcFuturesRepo) Update(_ *model.FuturesContract) error         { return nil }
func (m *secSvcFuturesRepo) UpsertByTicker(_ *model.FuturesContract) error { return nil }
func (m *secSvcFuturesRepo) List(_ repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
	return m.futures, int64(len(m.futures)), nil
}
func (m *secSvcFuturesRepo) UpdatePriceByTicker(_ string, _ decimal.Decimal) error { return nil }

type secSvcForexRepo struct {
	pairs  []model.ForexPair
	getErr error
}

func (m *secSvcForexRepo) Create(_ *model.ForexPair) error { return nil }
func (m *secSvcForexRepo) GetByID(id uint64) (*model.ForexPair, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.pairs {
		if m.pairs[i].ID == id {
			return &m.pairs[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcForexRepo) GetByTicker(_ string) (*model.ForexPair, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcForexRepo) Update(_ *model.ForexPair) error         { return nil }
func (m *secSvcForexRepo) UpsertByTicker(_ *model.ForexPair) error { return nil }
func (m *secSvcForexRepo) List(_ repository.ForexFilter) ([]model.ForexPair, int64, error) {
	return m.pairs, int64(len(m.pairs)), nil
}
func (m *secSvcForexRepo) UpdatePriceByTicker(_ string, _ decimal.Decimal) error { return nil }

type secSvcExchangeRepo struct{}

func (m *secSvcExchangeRepo) GetByID(_ uint64) (*model.StockExchange, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcExchangeRepo) GetByAcronym(_ string) (*model.StockExchange, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *secSvcExchangeRepo) List(_ string, _, _ int) ([]model.StockExchange, int64, error) {
	return nil, 0, nil
}

func newSecSvc(t *testing.T, stockRepo StockRepo, optionRepo OptionRepo, futuresRepo FuturesRepo, forexRepo ForexPairRepo) *SecurityService {
	t.Helper()
	return NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, &secSvcExchangeRepo{}, nil)
}

func TestSecurityService_NewSecurityService(t *testing.T) {
	svc := NewSecurityService(nil, nil, nil, nil, nil, nil)
	if svc == nil {
		t.Fatal("NewSecurityService returned nil")
	}
}

func TestSecurityService_ListStocks(t *testing.T) {
	stockRepo := &secSvcStockRepo{stocks: []model.Stock{{ID: 1}, {ID: 2}}}
	svc := newSecSvc(t, stockRepo, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	got, total, err := svc.ListStocks(repository.StockFilter{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("got %d/%d", total, len(got))
	}
}

func TestSecurityService_GetStock_Found(t *testing.T) {
	stockRepo := &secSvcStockRepo{stocks: []model.Stock{{ID: 1, Ticker: "AAPL"}}}
	svc := newSecSvc(t, stockRepo, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	got, err := svc.GetStock(1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Ticker != "AAPL" {
		t.Errorf("got %q", got.Ticker)
	}
}

func TestSecurityService_GetStock_NotFound(t *testing.T) {
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, err := svc.GetStock(99)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrStockNotFound) {
		t.Errorf("expected ErrStockNotFound, got %v", err)
	}
}

func TestSecurityService_GetStock_OtherErr(t *testing.T) {
	customErr := errors.New("db down")
	svc := newSecSvc(t, &secSvcStockRepo{getErr: customErr}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, err := svc.GetStock(1)
	if !errors.Is(err, customErr) {
		t.Errorf("got %v", err)
	}
}

func TestSecurityService_GetStockWithOptions(t *testing.T) {
	stockRepo := &secSvcStockRepo{stocks: []model.Stock{{ID: 1, Ticker: "AAPL"}}}
	optionRepo := &secSvcOptionRepo{options: []model.Option{{ID: 1, StockID: 1}, {ID: 2, StockID: 1}}}
	svc := newSecSvc(t, stockRepo, optionRepo, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	stock, opts, err := svc.GetStockWithOptions(1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stock == nil || len(opts) != 2 {
		t.Errorf("got %v / %d options", stock, len(opts))
	}
}

func TestSecurityService_GetStockWithOptions_StockNotFound(t *testing.T) {
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, _, err := svc.GetStockWithOptions(99)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSecurityService_GetStockWithOptions_OptionListError(t *testing.T) {
	stockRepo := &secSvcStockRepo{stocks: []model.Stock{{ID: 1}}}
	optionRepo := &secSvcOptionRepo{listErr: errors.New("list fail")}
	svc := newSecSvc(t, stockRepo, optionRepo, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, _, err := svc.GetStockWithOptions(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSecurityService_ListFutures(t *testing.T) {
	repo := &secSvcFuturesRepo{futures: []model.FuturesContract{{ID: 1}}}
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, repo, &secSvcForexRepo{})
	got, total, err := svc.ListFutures(repository.FuturesFilter{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Errorf("got %d/%d", total, len(got))
	}
}

func TestSecurityService_GetFutures_Found(t *testing.T) {
	repo := &secSvcFuturesRepo{futures: []model.FuturesContract{{ID: 1, Ticker: "CLJ26"}}}
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, repo, &secSvcForexRepo{})
	got, err := svc.GetFutures(1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Ticker != "CLJ26" {
		t.Errorf("got %q", got.Ticker)
	}
}

func TestSecurityService_GetFutures_NotFound(t *testing.T) {
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, err := svc.GetFutures(99)
	if err == nil || !errors.Is(err, ErrFuturesNotFound) {
		t.Errorf("expected ErrFuturesNotFound, got %v", err)
	}
}

func TestSecurityService_GetFutures_OtherErr(t *testing.T) {
	customErr := errors.New("db")
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{getErr: customErr}, &secSvcForexRepo{})
	_, err := svc.GetFutures(1)
	if !errors.Is(err, customErr) {
		t.Errorf("got %v", err)
	}
}

func TestSecurityService_ListForexPairs(t *testing.T) {
	repo := &secSvcForexRepo{pairs: []model.ForexPair{{ID: 1}}}
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, repo)
	got, total, err := svc.ListForexPairs(repository.ForexFilter{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Errorf("got %d/%d", total, len(got))
	}
}

func TestSecurityService_GetForexPair_Found(t *testing.T) {
	repo := &secSvcForexRepo{pairs: []model.ForexPair{{ID: 1, Ticker: "EUR/USD"}}}
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, repo)
	got, err := svc.GetForexPair(1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Ticker != "EUR/USD" {
		t.Errorf("got %q", got.Ticker)
	}
}

func TestSecurityService_GetForexPair_NotFound(t *testing.T) {
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, err := svc.GetForexPair(99)
	if err == nil || !errors.Is(err, ErrForexPairNotFound) {
		t.Errorf("expected ErrForexPairNotFound, got %v", err)
	}
}

func TestSecurityService_ListOptions(t *testing.T) {
	repo := &secSvcOptionRepo{options: []model.Option{{ID: 1}}}
	svc := newSecSvc(t, &secSvcStockRepo{}, repo, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	got, total, err := svc.ListOptions(repository.OptionFilter{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Errorf("got %d/%d", total, len(got))
	}
}

func TestSecurityService_GetOption_Found(t *testing.T) {
	repo := &secSvcOptionRepo{options: []model.Option{{ID: 1, Ticker: "AAPL260116C200"}}}
	svc := newSecSvc(t, &secSvcStockRepo{}, repo, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	got, err := svc.GetOption(1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Ticker != "AAPL260116C200" {
		t.Errorf("got %q", got.Ticker)
	}
}

func TestSecurityService_GetOption_NotFound(t *testing.T) {
	svc := newSecSvc(t, &secSvcStockRepo{}, &secSvcOptionRepo{}, &secSvcFuturesRepo{}, &secSvcForexRepo{})
	_, err := svc.GetOption(99)
	if err == nil || !errors.Is(err, ErrOptionNotFound) {
		t.Errorf("expected ErrOptionNotFound, got %v", err)
	}
}

func TestStockChangePercent_Normal(t *testing.T) {
	// price=110, change=10 → base=100 → 10/100*100 = 10%
	got := StockChangePercent(decimal.NewFromInt(110), decimal.NewFromInt(10))
	if !got.Equal(decimal.NewFromInt(10)) {
		t.Errorf("got %s want 10", got)
	}
}

func TestStockChangePercent_Negative(t *testing.T) {
	// price=90, change=-10 → base=100 → -10/100*100 = -10%
	got := StockChangePercent(decimal.NewFromInt(90), decimal.NewFromInt(-10))
	if !got.Equal(decimal.NewFromInt(-10)) {
		t.Errorf("got %s want -10", got)
	}
}

func TestStockChangePercent_ZeroBase(t *testing.T) {
	// price=10, change=10 → base=0 → result must be zero (avoid div-by-zero)
	got := StockChangePercent(decimal.NewFromInt(10), decimal.NewFromInt(10))
	if !got.IsZero() {
		t.Errorf("got %s want 0", got)
	}
}
