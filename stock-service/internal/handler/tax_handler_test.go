package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type mockTaxHandlerSvc struct {
	listFn        func(year, month int, filter service.TaxFilter) ([]service.TaxUserSummary, int64, error)
	getSummaryFn  func(userID uint64, systemType string) (decimal.Decimal, decimal.Decimal, error)
	listUserFn    func(userID uint64, systemType string, page, pageSize int) ([]model.CapitalGain, int64, error)
	listCollFn    func(userID uint64, systemType string) ([]model.TaxCollection, error)
	collectFn     func(year, month int) (int64, decimal.Decimal, int64, error)
}

func (m *mockTaxHandlerSvc) ListTaxRecords(year, month int, filter service.TaxFilter) ([]service.TaxUserSummary, int64, error) {
	if m.listFn != nil {
		return m.listFn(year, month, filter)
	}
	return nil, 0, nil
}

func (m *mockTaxHandlerSvc) GetUserTaxSummary(userID uint64, systemType string) (decimal.Decimal, decimal.Decimal, error) {
	if m.getSummaryFn != nil {
		return m.getSummaryFn(userID, systemType)
	}
	return decimal.Zero, decimal.Zero, nil
}

func (m *mockTaxHandlerSvc) ListUserTaxRecords(userID uint64, systemType string, page, pageSize int) ([]model.CapitalGain, int64, error) {
	if m.listUserFn != nil {
		return m.listUserFn(userID, systemType, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockTaxHandlerSvc) ListUserTaxCollections(userID uint64, systemType string) ([]model.TaxCollection, error) {
	if m.listCollFn != nil {
		return m.listCollFn(userID, systemType)
	}
	return nil, nil
}

func (m *mockTaxHandlerSvc) CollectTax(year, month int) (int64, decimal.Decimal, int64, error) {
	if m.collectFn != nil {
		return m.collectFn(year, month)
	}
	return 0, decimal.Zero, 0, nil
}

// ---------------------------------------------------------------------------
// ListTaxRecords
// ---------------------------------------------------------------------------

func TestTaxHandler_ListTaxRecords_Success(t *testing.T) {
	now := time.Now()
	svc := &mockTaxHandlerSvc{
		listFn: func(_, _ int, _ service.TaxFilter) ([]service.TaxUserSummary, int64, error) {
			return []service.TaxUserSummary{
				{
					UserID: 5, SystemType: "client", UserFirstName: "Alice", UserLastName: "Smith",
					TotalDebtRSD: decimal.NewFromFloat(150.5), LastCollection: &now,
				},
			}, 1, nil
		},
	}
	h := newTaxHandlerForTest(svc)
	resp, err := h.ListTaxRecords(context.Background(), &pb.ListTaxRecordsRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.TaxRecords) != 1 {
		t.Errorf("expected 1 record, got %d", len(resp.TaxRecords))
	}
	if resp.TaxRecords[0].FirstName != "Alice" {
		t.Errorf("expected Alice, got %s", resp.TaxRecords[0].FirstName)
	}
}

func TestTaxHandler_ListTaxRecords_NoLastCollection(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		listFn: func(_, _ int, _ service.TaxFilter) ([]service.TaxUserSummary, int64, error) {
			return []service.TaxUserSummary{
				{UserID: 5, SystemType: "client", TotalDebtRSD: decimal.Zero},
			}, 1, nil
		},
	}
	h := newTaxHandlerForTest(svc)
	resp, err := h.ListTaxRecords(context.Background(), &pb.ListTaxRecordsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TaxRecords[0].LastCollection != "" {
		t.Errorf("expected empty LastCollection, got %s", resp.TaxRecords[0].LastCollection)
	}
}

func TestTaxHandler_ListTaxRecords_Error(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		listFn: func(_, _ int, _ service.TaxFilter) ([]service.TaxUserSummary, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newTaxHandlerForTest(svc)
	_, err := h.ListTaxRecords(context.Background(), &pb.ListTaxRecordsRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// ListUserTaxRecords
// ---------------------------------------------------------------------------

func TestTaxHandler_ListUserTaxRecords_Success(t *testing.T) {
	now := time.Now()
	svc := &mockTaxHandlerSvc{
		listUserFn: func(_ uint64, _ string, _, _ int) ([]model.CapitalGain, int64, error) {
			return []model.CapitalGain{
				{
					ID: 1, SecurityType: "stock", Ticker: "AAPL", Quantity: 10,
					BuyPricePerUnit: decimal.NewFromFloat(100), SellPricePerUnit: decimal.NewFromFloat(150),
					TotalGain: decimal.NewFromFloat(500), Currency: "USD",
					TaxYear: 2025, TaxMonth: 12, CreatedAt: now,
				},
			}, 1, nil
		},
		getSummaryFn: func(_ uint64, _ string) (decimal.Decimal, decimal.Decimal, error) {
			return decimal.NewFromInt(50), decimal.NewFromInt(20), nil
		},
		listCollFn: func(_ uint64, _ string) ([]model.TaxCollection, error) {
			return []model.TaxCollection{
				{
					ID: 1, Year: 2025, Month: 11, AccountID: 100, Currency: "USD",
					TotalGain: decimal.NewFromInt(500), TaxAmount: decimal.NewFromInt(75),
					TaxAmountRSD: decimal.NewFromInt(8625), CollectedAt: now,
				},
			}, nil
		},
	}
	h := newTaxHandlerForTest(svc)
	resp, err := h.ListUserTaxRecords(context.Background(), &pb.ListUserTaxRecordsRequest{UserId: 5, SystemType: "client", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Records) != 1 {
		t.Errorf("expected 1 record, got %d", len(resp.Records))
	}
	if resp.TaxPaidThisYear != "50.00" {
		t.Errorf("expected TaxPaidThisYear=50.00, got %s", resp.TaxPaidThisYear)
	}
	if len(resp.Collections) != 1 {
		t.Errorf("expected 1 collection, got %d", len(resp.Collections))
	}
}

func TestTaxHandler_ListUserTaxRecords_DefaultsPagination(t *testing.T) {
	captured := struct{ page, size int }{}
	svc := &mockTaxHandlerSvc{
		listUserFn: func(_ uint64, _ string, page, pageSize int) ([]model.CapitalGain, int64, error) {
			captured.page = page
			captured.size = pageSize
			return nil, 0, nil
		},
	}
	h := newTaxHandlerForTest(svc)
	_, err := h.ListUserTaxRecords(context.Background(), &pb.ListUserTaxRecordsRequest{UserId: 5, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.page != 1 || captured.size != 10 {
		t.Errorf("expected page=1 size=10, got page=%d size=%d", captured.page, captured.size)
	}
}

func TestTaxHandler_ListUserTaxRecords_ListError(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		listUserFn: func(_ uint64, _ string, _, _ int) ([]model.CapitalGain, int64, error) {
			return nil, 0, errors.New("list failure")
		},
	}
	h := newTaxHandlerForTest(svc)
	_, err := h.ListUserTaxRecords(context.Background(), &pb.ListUserTaxRecordsRequest{UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestTaxHandler_ListUserTaxRecords_SummaryError(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		listUserFn: func(_ uint64, _ string, _, _ int) ([]model.CapitalGain, int64, error) {
			return nil, 0, nil
		},
		getSummaryFn: func(_ uint64, _ string) (decimal.Decimal, decimal.Decimal, error) {
			return decimal.Zero, decimal.Zero, errors.New("summary failure")
		},
	}
	h := newTaxHandlerForTest(svc)
	_, err := h.ListUserTaxRecords(context.Background(), &pb.ListUserTaxRecordsRequest{UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestTaxHandler_ListUserTaxRecords_CollectionsError(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		listUserFn: func(_ uint64, _ string, _, _ int) ([]model.CapitalGain, int64, error) {
			return nil, 0, nil
		},
		listCollFn: func(_ uint64, _ string) ([]model.TaxCollection, error) {
			return nil, errors.New("collections failure")
		},
	}
	h := newTaxHandlerForTest(svc)
	_, err := h.ListUserTaxRecords(context.Background(), &pb.ListUserTaxRecordsRequest{UserId: 5, SystemType: "client"})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// CollectTax
// ---------------------------------------------------------------------------

func TestTaxHandler_CollectTax_Success(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		collectFn: func(_, _ int) (int64, decimal.Decimal, int64, error) {
			return 5, decimal.NewFromInt(1250), 1, nil
		},
	}
	h := newTaxHandlerForTest(svc)
	resp, err := h.CollectTax(context.Background(), &pb.CollectTaxRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.CollectedCount != 5 || resp.FailedCount != 1 {
		t.Errorf("expected counts 5/1, got %d/%d", resp.CollectedCount, resp.FailedCount)
	}
	if resp.TotalCollectedRsd != "1250.00" {
		t.Errorf("expected total 1250.00, got %s", resp.TotalCollectedRsd)
	}
}

func TestTaxHandler_CollectTax_Error(t *testing.T) {
	svc := &mockTaxHandlerSvc{
		collectFn: func(_, _ int) (int64, decimal.Decimal, int64, error) {
			return 0, decimal.Zero, 0, errors.New("collection failed")
		},
	}
	h := newTaxHandlerForTest(svc)
	_, err := h.CollectTax(context.Background(), &pb.CollectTaxRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}
