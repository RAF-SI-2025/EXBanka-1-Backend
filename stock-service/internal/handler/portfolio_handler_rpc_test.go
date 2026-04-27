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

type mockPortfolioSvc struct {
	listFn       func(userID uint64, systemType string, filter service.HoldingFilter) ([]model.Holding, int64, error)
	priceFn      func(listingID uint64) (decimal.Decimal, error)
	makePubFn    func(holdingID, userID uint64, systemType string, quantity int64) (*model.Holding, error)
	exerciseFn   func(holdingID, userID uint64, systemType string) (*service.ExerciseResult, error)
	listTxFn     func(holdingID, userID uint64, systemType, direction string, page, pageSize int) ([]repository.HoldingTransactionRow, int64, error)
	exByOptionFn func(ctx context.Context, optionID, userID uint64, systemType string, holdingID uint64) (*service.ExerciseResult, error)
}

func (m *mockPortfolioSvc) ListHoldings(userID uint64, systemType string, filter service.HoldingFilter) ([]model.Holding, int64, error) {
	if m.listFn != nil {
		return m.listFn(userID, systemType, filter)
	}
	return nil, 0, nil
}

func (m *mockPortfolioSvc) GetCurrentPrice(listingID uint64) (decimal.Decimal, error) {
	if m.priceFn != nil {
		return m.priceFn(listingID)
	}
	return decimal.Zero, nil
}

func (m *mockPortfolioSvc) MakePublic(holdingID, userID uint64, systemType string, quantity int64) (*model.Holding, error) {
	if m.makePubFn != nil {
		return m.makePubFn(holdingID, userID, systemType, quantity)
	}
	return &model.Holding{ID: holdingID, PublicQuantity: quantity}, nil
}

func (m *mockPortfolioSvc) ExerciseOption(holdingID, userID uint64, systemType string) (*service.ExerciseResult, error) {
	if m.exerciseFn != nil {
		return m.exerciseFn(holdingID, userID, systemType)
	}
	return &service.ExerciseResult{ID: holdingID}, nil
}

func (m *mockPortfolioSvc) ListHoldingTransactions(holdingID, userID uint64, systemType, direction string, page, pageSize int) ([]repository.HoldingTransactionRow, int64, error) {
	if m.listTxFn != nil {
		return m.listTxFn(holdingID, userID, systemType, direction, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockPortfolioSvc) ExerciseOptionByOptionID(ctx context.Context, optionID, userID uint64, systemType string, holdingID uint64) (*service.ExerciseResult, error) {
	if m.exByOptionFn != nil {
		return m.exByOptionFn(ctx, optionID, userID, systemType, holdingID)
	}
	return &service.ExerciseResult{ID: optionID}, nil
}

type mockTaxSvc struct {
	gainsFn func(userID uint64, systemType string) (service.UserGainsAndTax, error)
}

func (m *mockTaxSvc) GetUserGainsAndTax(userID uint64, systemType string) (service.UserGainsAndTax, error) {
	if m.gainsFn != nil {
		return m.gainsFn(userID, systemType)
	}
	return service.UserGainsAndTax{}, nil
}

// ---------------------------------------------------------------------------
// ListHoldings
// ---------------------------------------------------------------------------

func TestPortfolioHandler_ListHoldings_Success(t *testing.T) {
	now := time.Now()
	psvc := &mockPortfolioSvc{
		listFn: func(_ uint64, _ string, _ service.HoldingFilter) ([]model.Holding, int64, error) {
			return []model.Holding{
				{ID: 1, SecurityType: "stock", Ticker: "AAPL", Name: "Apple", Quantity: 10, PublicQuantity: 0, AccountID: 100, UpdatedAt: now},
			}, 1, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	resp, err := h.ListHoldings(context.Background(), &pb.ListHoldingsRequest{UserId: 5, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Holdings) != 1 {
		t.Errorf("expected 1 holding, got %d (count %d)", len(resp.Holdings), resp.TotalCount)
	}
	if resp.Holdings[0].Ticker != "AAPL" {
		t.Errorf("unexpected ticker: %s", resp.Holdings[0].Ticker)
	}
}

func TestPortfolioHandler_ListHoldings_ServiceError(t *testing.T) {
	psvc := &mockPortfolioSvc{
		listFn: func(_ uint64, _ string, _ service.HoldingFilter) ([]model.Holding, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.ListHoldings(context.Background(), &pb.ListHoldingsRequest{UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetPortfolioSummary
// ---------------------------------------------------------------------------

func TestPortfolioHandler_GetPortfolioSummary_Success(t *testing.T) {
	psvc := &mockPortfolioSvc{
		listFn: func(_ uint64, _ string, _ service.HoldingFilter) ([]model.Holding, int64, error) {
			return []model.Holding{
				{ID: 1, ListingID: 50, Quantity: 10, AveragePrice: decimal.NewFromInt(100)},
				{ID: 2, ListingID: 51, Quantity: 0, AveragePrice: decimal.NewFromInt(200)}, // skipped
			}, 2, nil
		},
		priceFn: func(_ uint64) (decimal.Decimal, error) {
			return decimal.NewFromInt(110), nil
		},
	}
	tsvc := &mockTaxSvc{
		gainsFn: func(_ uint64, _ string) (service.UserGainsAndTax, error) {
			return service.UserGainsAndTax{
				RealizedGainLifetimeRSD: decimal.NewFromInt(500),
				TaxPaidThisYearRSD:      decimal.NewFromInt(20),
				ClosedTradesThisYear:    3,
			}, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, tsvc)
	resp, err := h.GetPortfolioSummary(context.Background(), &pb.GetPortfolioSummaryRequest{UserId: 5, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OpenPositionsCount != 1 {
		t.Errorf("expected 1 open position, got %d", resp.OpenPositionsCount)
	}
	if resp.UnrealizedProfit != "100.00" {
		t.Errorf("expected unrealized 100.00, got %s", resp.UnrealizedProfit)
	}
	if resp.ClosedTradesThisYear != 3 {
		t.Errorf("expected ClosedTradesThisYear=3, got %d", resp.ClosedTradesThisYear)
	}
}

func TestPortfolioHandler_GetPortfolioSummary_PriceErrorSkipsHolding(t *testing.T) {
	psvc := &mockPortfolioSvc{
		listFn: func(_ uint64, _ string, _ service.HoldingFilter) ([]model.Holding, int64, error) {
			return []model.Holding{{ID: 1, ListingID: 50, Quantity: 10, AveragePrice: decimal.NewFromInt(100)}}, 1, nil
		},
		priceFn: func(_ uint64) (decimal.Decimal, error) {
			return decimal.Zero, errors.New("price unavailable")
		},
	}
	tsvc := &mockTaxSvc{}
	h := newPortfolioHandlerForTest(psvc, tsvc)
	resp, err := h.GetPortfolioSummary(context.Background(), &pb.GetPortfolioSummaryRequest{UserId: 5, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UnrealizedProfit != "0.00" {
		t.Errorf("expected unrealized 0.00, got %s", resp.UnrealizedProfit)
	}
}

func TestPortfolioHandler_GetPortfolioSummary_ListError(t *testing.T) {
	psvc := &mockPortfolioSvc{
		listFn: func(_ uint64, _ string, _ service.HoldingFilter) ([]model.Holding, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.GetPortfolioSummary(context.Background(), &pb.GetPortfolioSummaryRequest{UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestPortfolioHandler_GetPortfolioSummary_TaxError(t *testing.T) {
	psvc := &mockPortfolioSvc{
		listFn: func(_ uint64, _ string, _ service.HoldingFilter) ([]model.Holding, int64, error) {
			return nil, 0, nil
		},
	}
	tsvc := &mockTaxSvc{
		gainsFn: func(_ uint64, _ string) (service.UserGainsAndTax, error) {
			return service.UserGainsAndTax{}, errors.New("tax svc down")
		},
	}
	h := newPortfolioHandlerForTest(psvc, tsvc)
	_, err := h.GetPortfolioSummary(context.Background(), &pb.GetPortfolioSummaryRequest{UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// MakePublic
// ---------------------------------------------------------------------------

func TestPortfolioHandler_MakePublic_Success(t *testing.T) {
	now := time.Now()
	psvc := &mockPortfolioSvc{
		makePubFn: func(holdingID, _ uint64, _ string, qty int64) (*model.Holding, error) {
			return &model.Holding{
				ID: holdingID, SecurityType: "stock", Ticker: "AAPL", Name: "Apple",
				Quantity: 10, AveragePrice: decimal.NewFromInt(150), PublicQuantity: qty, AccountID: 100, UpdatedAt: now,
			}, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	resp, err := h.MakePublic(context.Background(), &pb.MakePublicRequest{HoldingId: 1, UserId: 5, SystemType: "client", Quantity: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.PublicQuantity != 5 {
		t.Errorf("expected PublicQuantity=5, got %d", resp.PublicQuantity)
	}
}

func TestPortfolioHandler_MakePublic_NotFound(t *testing.T) {
	psvc := &mockPortfolioSvc{
		makePubFn: func(_, _ uint64, _ string, _ int64) (*model.Holding, error) {
			return nil, fmt.Errorf("holding not found: %w", service.ErrHoldingNotFound)
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.MakePublic(context.Background(), &pb.MakePublicRequest{HoldingId: 9, UserId: 5, SystemType: "client", Quantity: 5})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestPortfolioHandler_MakePublic_PermissionDenied(t *testing.T) {
	psvc := &mockPortfolioSvc{
		makePubFn: func(_, _ uint64, _ string, _ int64) (*model.Holding, error) {
			return nil, fmt.Errorf("holding does not belong to user: %w", service.ErrHoldingOwnership)
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.MakePublic(context.Background(), &pb.MakePublicRequest{HoldingId: 1, UserId: 99, SystemType: "client", Quantity: 5})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", status.Code(err))
	}
}

func TestPortfolioHandler_MakePublic_FailedPrecondition(t *testing.T) {
	psvc := &mockPortfolioSvc{
		makePubFn: func(_, _ uint64, _ string, _ int64) (*model.Holding, error) {
			return nil, fmt.Errorf("only stocks can be made public for OTC trading: %w", service.ErrPublicOnlyStocks)
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.MakePublic(context.Background(), &pb.MakePublicRequest{HoldingId: 1, UserId: 5, SystemType: "client", Quantity: 5})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// ExerciseOption
// ---------------------------------------------------------------------------

func TestPortfolioHandler_ExerciseOption_Success(t *testing.T) {
	psvc := &mockPortfolioSvc{
		exerciseFn: func(holdingID, _ uint64, _ string) (*service.ExerciseResult, error) {
			return &service.ExerciseResult{
				ID: holdingID, OptionTicker: "AAPL_20260101_C150",
				ExercisedQuantity: 10, SharesAffected: 1000,
				Profit: decimal.NewFromInt(500),
			}, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	resp, err := h.ExerciseOption(context.Background(), &pb.ExerciseOptionRequest{HoldingId: 1, UserId: 5, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OptionTicker != "AAPL_20260101_C150" {
		t.Errorf("unexpected OptionTicker: %s", resp.OptionTicker)
	}
	if resp.Profit != "500.00" {
		t.Errorf("expected profit 500.00, got %s", resp.Profit)
	}
}

func TestPortfolioHandler_ExerciseOption_Expired(t *testing.T) {
	psvc := &mockPortfolioSvc{
		exerciseFn: func(_, _ uint64, _ string) (*service.ExerciseResult, error) {
			return nil, fmt.Errorf("option has expired (settlement date passed): %w", service.ErrOptionExpired)
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.ExerciseOption(context.Background(), &pb.ExerciseOptionRequest{HoldingId: 1, UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestPortfolioHandler_ExerciseOption_Internal(t *testing.T) {
	// A bare error from the service surfaces as codes.Unknown — handlers no
	// longer rewrite to Internal. Either is acceptable as a "non-business"
	// signal; we just check it's not OK.
	psvc := &mockPortfolioSvc{
		exerciseFn: func(_, _ uint64, _ string) (*service.ExerciseResult, error) {
			return nil, errors.New("unexpected db error")
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.ExerciseOption(context.Background(), &pb.ExerciseOptionRequest{HoldingId: 1, UserId: 5, SystemType: "client"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// ListHoldingTransactions
// ---------------------------------------------------------------------------

func TestPortfolioHandler_ListHoldingTransactions_Success(t *testing.T) {
	native := decimal.NewFromFloat(150.5)
	converted := decimal.NewFromFloat(17500.0)
	fx := decimal.NewFromFloat(116.28)
	now := time.Now()
	psvc := &mockPortfolioSvc{
		listTxFn: func(_, _ uint64, _, _ string, _, _ int) ([]repository.HoldingTransactionRow, int64, error) {
			return []repository.HoldingTransactionRow{
				{
					ID: 1, OrderID: 100, ExecutedAt: now, Direction: "buy",
					Quantity: 10, PricePerUnit: decimal.NewFromFloat(150.5),
					NativeAmount: &native, NativeCurrency: "USD",
					ConvertedAmount: &converted, AccountCurrency: "RSD",
					FxRate: &fx, Commission: decimal.NewFromFloat(2.5), AccountID: 100, Ticker: "AAPL",
				},
			}, 1, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	resp, err := h.ListHoldingTransactions(context.Background(), &pb.ListHoldingTransactionsRequest{HoldingId: 1, UserId: 5, SystemType: "client", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Transactions) != 1 {
		t.Errorf("expected 1 transaction, got %d", len(resp.Transactions))
	}
	if resp.Transactions[0].Ticker != "AAPL" {
		t.Errorf("unexpected ticker: %s", resp.Transactions[0].Ticker)
	}
	if resp.Transactions[0].NativeAmount == "" {
		t.Errorf("expected NativeAmount populated")
	}
	if resp.Transactions[0].FxRate == "" {
		t.Errorf("expected FxRate populated")
	}
}

func TestPortfolioHandler_ListHoldingTransactions_DefaultsPagination(t *testing.T) {
	captured := struct{ page, size int }{}
	psvc := &mockPortfolioSvc{
		listTxFn: func(_, _ uint64, _, _ string, page, pageSize int) ([]repository.HoldingTransactionRow, int64, error) {
			captured.page = page
			captured.size = pageSize
			return nil, 0, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.ListHoldingTransactions(context.Background(), &pb.ListHoldingTransactionsRequest{HoldingId: 1, UserId: 5, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.page != 1 || captured.size != 10 {
		t.Errorf("expected page=1 size=10, got page=%d size=%d", captured.page, captured.size)
	}
}

func TestPortfolioHandler_ListHoldingTransactions_NotFound(t *testing.T) {
	psvc := &mockPortfolioSvc{
		listTxFn: func(_, _ uint64, _, _ string, _, _ int) ([]repository.HoldingTransactionRow, int64, error) {
			return nil, 0, fmt.Errorf("holding not found: %w", service.ErrHoldingNotFound)
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.ListHoldingTransactions(context.Background(), &pb.ListHoldingTransactionsRequest{HoldingId: 9, UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// ExerciseOptionByOptionID
// ---------------------------------------------------------------------------

func TestPortfolioHandler_ExerciseOptionByOptionID_Success(t *testing.T) {
	psvc := &mockPortfolioSvc{
		exByOptionFn: func(_ context.Context, optionID, _ uint64, _ string, _ uint64) (*service.ExerciseResult, error) {
			return &service.ExerciseResult{
				ID: optionID, OptionTicker: "TSLA_20260101_P200",
				ExercisedQuantity: 5, SharesAffected: 500,
				Profit: decimal.NewFromInt(250),
			}, nil
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	resp, err := h.ExerciseOptionByOptionID(context.Background(), &pb.ExerciseOptionByOptionIDRequest{OptionId: 7, UserId: 5, SystemType: "client", HoldingId: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != 7 {
		t.Errorf("expected ID=7, got %d", resp.Id)
	}
	if resp.Profit != "250.00" {
		t.Errorf("expected profit 250.00, got %s", resp.Profit)
	}
}

func TestPortfolioHandler_ExerciseOptionByOptionID_NotFound(t *testing.T) {
	psvc := &mockPortfolioSvc{
		exByOptionFn: func(_ context.Context, _, _ uint64, _ string, _ uint64) (*service.ExerciseResult, error) {
			return nil, fmt.Errorf("option not found: %w", service.ErrOptionNotFound)
		},
	}
	h := newPortfolioHandlerForTest(psvc, &mockTaxSvc{})
	_, err := h.ExerciseOptionByOptionID(context.Background(), &pb.ExerciseOptionByOptionIDRequest{OptionId: 99, UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}
