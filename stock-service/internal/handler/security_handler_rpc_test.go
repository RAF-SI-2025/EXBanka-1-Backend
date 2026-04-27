package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockSecuritySvc struct {
	listStocksFn      func(filter repository.StockFilter) ([]model.Stock, int64, error)
	getStockFn        func(id uint64) (*model.Stock, []model.Option, error)
	listFuturesFn     func(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error)
	getFuturesFn      func(id uint64) (*model.FuturesContract, error)
	listForexFn       func(filter repository.ForexFilter) ([]model.ForexPair, int64, error)
	getForexFn        func(id uint64) (*model.ForexPair, error)
	listOptionsFn     func(filter repository.OptionFilter) ([]model.Option, int64, error)
	getOptionFn       func(id uint64) (*model.Option, error)
}

func (m *mockSecuritySvc) ListStocks(filter repository.StockFilter) ([]model.Stock, int64, error) {
	if m.listStocksFn != nil {
		return m.listStocksFn(filter)
	}
	return nil, 0, nil
}

func (m *mockSecuritySvc) GetStockWithOptions(id uint64) (*model.Stock, []model.Option, error) {
	if m.getStockFn != nil {
		return m.getStockFn(id)
	}
	return &model.Stock{ID: id}, nil, nil
}

func (m *mockSecuritySvc) ListFutures(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
	if m.listFuturesFn != nil {
		return m.listFuturesFn(filter)
	}
	return nil, 0, nil
}

func (m *mockSecuritySvc) GetFutures(id uint64) (*model.FuturesContract, error) {
	if m.getFuturesFn != nil {
		return m.getFuturesFn(id)
	}
	return &model.FuturesContract{ID: id}, nil
}

func (m *mockSecuritySvc) ListForexPairs(filter repository.ForexFilter) ([]model.ForexPair, int64, error) {
	if m.listForexFn != nil {
		return m.listForexFn(filter)
	}
	return nil, 0, nil
}

func (m *mockSecuritySvc) GetForexPair(id uint64) (*model.ForexPair, error) {
	if m.getForexFn != nil {
		return m.getForexFn(id)
	}
	return &model.ForexPair{ID: id}, nil
}

func (m *mockSecuritySvc) ListOptions(filter repository.OptionFilter) ([]model.Option, int64, error) {
	if m.listOptionsFn != nil {
		return m.listOptionsFn(filter)
	}
	return nil, 0, nil
}

func (m *mockSecuritySvc) GetOption(id uint64) (*model.Option, error) {
	if m.getOptionFn != nil {
		return m.getOptionFn(id)
	}
	return &model.Option{ID: id}, nil
}

type mockListingSvc struct {
	histFn func(securityID uint64, securityType, period string, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error)
}

func (m *mockListingSvc) GetPriceHistoryForSecurity(securityID uint64, securityType, period string, page, pageSize int) ([]model.ListingDailyPriceInfo, int64, error) {
	if m.histFn != nil {
		return m.histFn(securityID, securityType, period, page, pageSize)
	}
	return nil, 0, nil
}

type mockCandleSvc struct {
	getFn func(ctx context.Context, listingID uint64, interval string, from, to time.Time) ([]service.CandlePoint, error)
}

func (m *mockCandleSvc) GetCandles(ctx context.Context, listingID uint64, interval string, from, to time.Time) ([]service.CandlePoint, error) {
	if m.getFn != nil {
		return m.getFn(ctx, listingID, interval, from, to)
	}
	return nil, nil
}

type mockListingRepo struct {
	getFn  func(securityID uint64, securityType string) (*model.Listing, error)
	listFn func(securityIDs []uint64, securityType string) ([]model.Listing, error)
}

func (m *mockListingRepo) GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	if m.getFn != nil {
		return m.getFn(securityID, securityType)
	}
	return &model.Listing{ID: 100}, nil
}

func (m *mockListingRepo) ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error) {
	if m.listFn != nil {
		return m.listFn(securityIDs, securityType)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// ListStocks
// ---------------------------------------------------------------------------

func TestSecurityHandler_ListStocks_Success(t *testing.T) {
	now := time.Now()
	sec := &mockSecuritySvc{
		listStocksFn: func(_ repository.StockFilter) ([]model.Stock, int64, error) {
			return []model.Stock{{ID: 1, Ticker: "AAPL", Name: "Apple", Price: decimal.NewFromFloat(150), LastRefresh: now}}, 1, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.ListStocks(context.Background(), &pb.ListStocksRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Stocks) != 1 {
		t.Errorf("expected 1 stock, got %d", len(resp.Stocks))
	}
}

func TestSecurityHandler_ListStocks_BadMinPrice(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListStocks(context.Background(), &pb.ListStocksRequest{MinPrice: "not-a-number"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_ListStocks_BadMaxPrice(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListStocks(context.Background(), &pb.ListStocksRequest{MaxPrice: "not-a-number"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_ListStocks_WithFilters(t *testing.T) {
	captured := repository.StockFilter{}
	sec := &mockSecuritySvc{
		listStocksFn: func(filter repository.StockFilter) ([]model.Stock, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListStocks(context.Background(), &pb.ListStocksRequest{
		MinPrice: "10", MaxPrice: "200", MinVolume: 1000, MaxVolume: 1000000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.MinPrice == nil || captured.MaxPrice == nil || captured.MinVolume == nil || captured.MaxVolume == nil {
		t.Error("expected all filters populated")
	}
}

func TestSecurityHandler_ListStocks_ServiceError(t *testing.T) {
	sec := &mockSecuritySvc{
		listStocksFn: func(_ repository.StockFilter) ([]model.Stock, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListStocks(context.Background(), &pb.ListStocksRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetStock
// ---------------------------------------------------------------------------

func TestSecurityHandler_GetStock_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		getStockFn: func(id uint64) (*model.Stock, []model.Option, error) {
			return &model.Stock{ID: id, Ticker: "AAPL", Name: "Apple", Price: decimal.NewFromFloat(150)}, nil, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.GetStock(context.Background(), &pb.GetStockRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Ticker != "AAPL" {
		t.Errorf("unexpected ticker: %s", resp.Ticker)
	}
}

func TestSecurityHandler_GetStock_NotFound(t *testing.T) {
	sec := &mockSecuritySvc{
		getStockFn: func(_ uint64) (*model.Stock, []model.Option, error) {
			return nil, nil, fmt.Errorf("stock not found: %w", service.ErrStockNotFound)
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetStock(context.Background(), &pb.GetStockRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetStockHistory
// ---------------------------------------------------------------------------

func TestSecurityHandler_GetStockHistory_Success(t *testing.T) {
	now := time.Now()
	listingSvc := &mockListingSvc{
		histFn: func(_ uint64, _, _ string, _, _ int) ([]model.ListingDailyPriceInfo, int64, error) {
			return []model.ListingDailyPriceInfo{
				{Date: now, Price: decimal.NewFromFloat(150), High: decimal.NewFromFloat(155), Low: decimal.NewFromFloat(148)},
			}, 1, nil
		},
	}
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, listingSvc, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.GetStockHistory(context.Background(), &pb.GetPriceHistoryRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("expected 1 entry, got %d", resp.TotalCount)
	}
}

func TestSecurityHandler_GetStockHistory_Error(t *testing.T) {
	listingSvc := &mockListingSvc{
		histFn: func(_ uint64, _, _ string, _, _ int) ([]model.ListingDailyPriceInfo, int64, error) {
			return nil, 0, fmt.Errorf("listing not found: %w", service.ErrListingNotFound)
		},
	}
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, listingSvc, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetStockHistory(context.Background(), &pb.GetPriceHistoryRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// ListFutures / GetFutures / GetFuturesHistory
// ---------------------------------------------------------------------------

func TestSecurityHandler_ListFutures_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		listFuturesFn: func(_ repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
			return []model.FuturesContract{{ID: 1, Ticker: "ESF24", Name: "S&P E-mini"}}, 1, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.ListFutures(context.Background(), &pb.ListFuturesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("expected 1, got %d", resp.TotalCount)
	}
}

func TestSecurityHandler_ListFutures_BadSettlementDateFrom(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListFutures(context.Background(), &pb.ListFuturesRequest{SettlementDateFrom: "bogus"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_ListFutures_BadSettlementDateTo(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListFutures(context.Background(), &pb.ListFuturesRequest{SettlementDateTo: "bogus"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_ListFutures_ValidDates(t *testing.T) {
	captured := repository.FuturesFilter{}
	sec := &mockSecuritySvc{
		listFuturesFn: func(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListFutures(context.Background(), &pb.ListFuturesRequest{
		SettlementDateFrom: "2026-01-01", SettlementDateTo: "2026-12-31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.SettlementDateFrom == nil || captured.SettlementDateTo == nil {
		t.Error("expected dates populated")
	}
}

func TestSecurityHandler_GetFutures_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		getFuturesFn: func(id uint64) (*model.FuturesContract, error) {
			return &model.FuturesContract{ID: id, Ticker: "ESF24", SettlementDate: time.Now()}, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.GetFutures(context.Background(), &pb.GetFuturesRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Ticker != "ESF24" {
		t.Errorf("unexpected ticker: %s", resp.Ticker)
	}
}

func TestSecurityHandler_GetFutures_NotFound(t *testing.T) {
	sec := &mockSecuritySvc{
		getFuturesFn: func(_ uint64) (*model.FuturesContract, error) {
			return nil, fmt.Errorf("futures not found: %w", service.ErrFuturesNotFound)
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetFutures(context.Background(), &pb.GetFuturesRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetFuturesHistory_Success(t *testing.T) {
	listingSvc := &mockListingSvc{
		histFn: func(_ uint64, secType, _ string, _, _ int) ([]model.ListingDailyPriceInfo, int64, error) {
			if secType != "futures" {
				t.Errorf("expected secType=futures, got %s", secType)
			}
			return nil, 0, nil
		},
	}
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, listingSvc, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetFuturesHistory(context.Background(), &pb.GetPriceHistoryRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ListForexPairs / GetForexPair / GetForexPairHistory
// ---------------------------------------------------------------------------

func TestSecurityHandler_ListForexPairs_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		listForexFn: func(_ repository.ForexFilter) ([]model.ForexPair, int64, error) {
			return []model.ForexPair{{ID: 1, Ticker: "EURUSD", BaseCurrency: "EUR", QuoteCurrency: "USD"}}, 1, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.ListForexPairs(context.Background(), &pb.ListForexPairsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("expected 1, got %d", resp.TotalCount)
	}
}

func TestSecurityHandler_ListForexPairs_Error(t *testing.T) {
	sec := &mockSecuritySvc{
		listForexFn: func(_ repository.ForexFilter) ([]model.ForexPair, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListForexPairs(context.Background(), &pb.ListForexPairsRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetForexPair_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		getForexFn: func(id uint64) (*model.ForexPair, error) {
			return &model.ForexPair{ID: id, Ticker: "EURUSD"}, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.GetForexPair(context.Background(), &pb.GetForexPairRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Ticker != "EURUSD" {
		t.Errorf("unexpected ticker: %s", resp.Ticker)
	}
}

func TestSecurityHandler_GetForexPair_NotFound(t *testing.T) {
	sec := &mockSecuritySvc{
		getForexFn: func(_ uint64) (*model.ForexPair, error) {
			return nil, fmt.Errorf("forex pair not found: %w", service.ErrForexPairNotFound)
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetForexPair(context.Background(), &pb.GetForexPairRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetForexPairHistory_Success(t *testing.T) {
	listingSvc := &mockListingSvc{
		histFn: func(_ uint64, secType, _ string, _, _ int) ([]model.ListingDailyPriceInfo, int64, error) {
			if secType != "forex" {
				t.Errorf("expected secType=forex, got %s", secType)
			}
			return nil, 0, nil
		},
	}
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, listingSvc, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetForexPairHistory(context.Background(), &pb.GetPriceHistoryRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ListOptions / GetOption
// ---------------------------------------------------------------------------

func TestSecurityHandler_ListOptions_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		listOptionsFn: func(_ repository.OptionFilter) ([]model.Option, int64, error) {
			return []model.Option{{ID: 1, Ticker: "AAPL_C150", OptionType: "call", StrikePrice: decimal.NewFromInt(150)}}, 1, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.ListOptions(context.Background(), &pb.ListOptionsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("expected 1, got %d", resp.TotalCount)
	}
}

func TestSecurityHandler_ListOptions_BadSettlementDate(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListOptions(context.Background(), &pb.ListOptionsRequest{SettlementDate: "bogus"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_ListOptions_WithFilters(t *testing.T) {
	captured := repository.OptionFilter{}
	sec := &mockSecuritySvc{
		listOptionsFn: func(filter repository.OptionFilter) ([]model.Option, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListOptions(context.Background(), &pb.ListOptionsRequest{
		StockId: 5, OptionType: "call", SettlementDate: "2026-01-01",
		MinStrike: "100", MaxStrike: "200",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.StockID == nil || captured.OptionType != "call" || captured.SettlementDate == nil {
		t.Error("expected filters populated")
	}
}

func TestSecurityHandler_ListOptions_Error(t *testing.T) {
	sec := &mockSecuritySvc{
		listOptionsFn: func(_ repository.OptionFilter) ([]model.Option, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.ListOptions(context.Background(), &pb.ListOptionsRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetOption_Success(t *testing.T) {
	sec := &mockSecuritySvc{
		getOptionFn: func(id uint64) (*model.Option, error) {
			return &model.Option{ID: id, Ticker: "AAPL_C150", OptionType: "call", StrikePrice: decimal.NewFromInt(150)}, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	resp, err := h.GetOption(context.Background(), &pb.GetOptionRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Ticker != "AAPL_C150" {
		t.Errorf("unexpected ticker: %s", resp.Ticker)
	}
}

func TestSecurityHandler_GetOption_NotFound(t *testing.T) {
	sec := &mockSecuritySvc{
		getOptionFn: func(_ uint64) (*model.Option, error) {
			return nil, fmt.Errorf("option not found: %w", service.ErrOptionNotFound)
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetOption(context.Background(), &pb.GetOptionRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetCandles
// ---------------------------------------------------------------------------

func TestSecurityHandler_GetCandles_Success(t *testing.T) {
	candleSvc := &mockCandleSvc{
		getFn: func(_ context.Context, listingID uint64, _ string, _, _ time.Time) ([]service.CandlePoint, error) {
			return []service.CandlePoint{
				{Timestamp: time.Now(), Open: 150.0, High: 155.0, Low: 148.0, Close: 152.0, Volume: 10000},
			}, nil
		},
	}
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, candleSvc, &mockListingRepo{})
	resp, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1, Interval: "1d",
		From: "2026-01-01T00:00:00Z", To: "2026-01-31T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("expected 1 candle, got %d", resp.Count)
	}
}

func TestSecurityHandler_GetCandles_MissingListingID(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{Interval: "1d", From: "2026-01-01T00:00:00Z", To: "2026-01-31T00:00:00Z"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetCandles_MissingInterval(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{ListingId: 1, From: "2026-01-01T00:00:00Z", To: "2026-01-31T00:00:00Z"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetCandles_BadFrom(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{ListingId: 1, Interval: "1d", From: "bogus", To: "2026-01-31T00:00:00Z"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetCandles_BadTo(t *testing.T) {
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{ListingId: 1, Interval: "1d", From: "2026-01-01T00:00:00Z", To: "bogus"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestSecurityHandler_GetCandles_ServiceError(t *testing.T) {
	candleSvc := &mockCandleSvc{
		getFn: func(_ context.Context, _ uint64, _ string, _, _ time.Time) ([]service.CandlePoint, error) {
			return nil, errors.New("candle source unavailable")
		},
	}
	h := newSecurityHandlerForTest(&mockSecuritySvc{}, &mockListingSvc{}, candleSvc, &mockListingRepo{})
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1, Interval: "1d", From: "2026-01-01T00:00:00Z", To: "2026-01-31T00:00:00Z",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
