package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type mockExchangeSvc struct {
	listFn       func(search string, page, pageSize int) ([]model.StockExchange, int64, error)
	getFn        func(id uint64) (*model.StockExchange, error)
	setTestingFn func(enabled bool) error
	getTestingFn func() bool
}

func (m *mockExchangeSvc) ListExchanges(search string, page, pageSize int) ([]model.StockExchange, int64, error) {
	if m.listFn != nil {
		return m.listFn(search, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockExchangeSvc) GetExchange(id uint64) (*model.StockExchange, error) {
	if m.getFn != nil {
		return m.getFn(id)
	}
	return &model.StockExchange{ID: id}, nil
}

func (m *mockExchangeSvc) SetTestingMode(enabled bool) error {
	if m.setTestingFn != nil {
		return m.setTestingFn(enabled)
	}
	return nil
}

func (m *mockExchangeSvc) GetTestingMode() bool {
	if m.getTestingFn != nil {
		return m.getTestingFn()
	}
	return false
}

func TestExchangeHandler_ListExchanges_Success(t *testing.T) {
	svc := &mockExchangeSvc{
		listFn: func(_ string, _, _ int) ([]model.StockExchange, int64, error) {
			return []model.StockExchange{
				{ID: 1, Name: "NYSE", Acronym: "NYSE", MICCode: "XNYS", Currency: "USD"},
			}, 1, nil
		},
	}
	h := newExchangeHandlerForTest(svc)
	resp, err := h.ListExchanges(context.Background(), &pb.ListExchangesRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Exchanges) != 1 {
		t.Errorf("expected 1 exchange, got %d", len(resp.Exchanges))
	}
	if resp.Exchanges[0].Name != "NYSE" {
		t.Errorf("unexpected name: %s", resp.Exchanges[0].Name)
	}
}

func TestExchangeHandler_ListExchanges_Error(t *testing.T) {
	// Service signals the failure via a typed sentinel; handler passthrough
	// surfaces the embedded code on the wire.
	svc := &mockExchangeSvc{
		listFn: func(_ string, _, _ int) ([]model.StockExchange, int64, error) {
			return nil, 0, fmt.Errorf("db down: %w", service.ErrExchangeNotFound)
		},
	}
	h := newExchangeHandlerForTest(svc)
	_, err := h.ListExchanges(context.Background(), &pb.ListExchangesRequest{})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound (sentinel propagated), got %v", status.Code(err))
	}
}

func TestExchangeHandler_GetExchange_Success(t *testing.T) {
	svc := &mockExchangeSvc{
		getFn: func(id uint64) (*model.StockExchange, error) {
			return &model.StockExchange{ID: id, Name: "LSE", Acronym: "LSE"}, nil
		},
	}
	h := newExchangeHandlerForTest(svc)
	resp, err := h.GetExchange(context.Background(), &pb.GetExchangeRequest{Id: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != 5 || resp.Name != "LSE" {
		t.Errorf("unexpected exchange: %+v", resp)
	}
}

func TestExchangeHandler_GetExchange_NotFound(t *testing.T) {
	svc := &mockExchangeSvc{
		getFn: func(_ uint64) (*model.StockExchange, error) {
			return nil, fmt.Errorf("exchange not found: %w", service.ErrExchangeNotFound)
		},
	}
	h := newExchangeHandlerForTest(svc)
	_, err := h.GetExchange(context.Background(), &pb.GetExchangeRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestExchangeHandler_SetTestingMode_Success(t *testing.T) {
	captured := false
	svc := &mockExchangeSvc{
		setTestingFn: func(enabled bool) error {
			captured = enabled
			return nil
		},
	}
	h := newExchangeHandlerForTest(svc)
	resp, err := h.SetTestingMode(context.Background(), &pb.SetTestingModeRequest{Enabled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !captured {
		t.Errorf("expected service called with enabled=true")
	}
	if !resp.TestingMode {
		t.Errorf("expected response TestingMode=true")
	}
}

func TestExchangeHandler_SetTestingMode_Error(t *testing.T) {
	svc := &mockExchangeSvc{
		setTestingFn: func(_ bool) error {
			return errors.New("settings repo down")
		},
	}
	h := newExchangeHandlerForTest(svc)
	_, err := h.SetTestingMode(context.Background(), &pb.SetTestingModeRequest{Enabled: true})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestExchangeHandler_GetTestingMode_Success(t *testing.T) {
	svc := &mockExchangeSvc{
		getTestingFn: func() bool { return true },
	}
	h := newExchangeHandlerForTest(svc)
	resp, err := h.GetTestingMode(context.Background(), &pb.GetTestingModeRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.TestingMode {
		t.Errorf("expected TestingMode=true")
	}
}

// Sentinel passthrough sanity check — replaces the deleted TestMapServiceError
// suite. Service-layer sentinels carry their own gRPC code via
// svcerr.SentinelError; handlers no longer need a per-message map.
func TestSentinels_PassthroughCodes_ExchangeAndSecurity(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		code     codes.Code
	}{
		{"ExchangeNotFound", service.ErrExchangeNotFound, codes.NotFound},
		{"StockNotFound", service.ErrStockNotFound, codes.NotFound},
		{"FuturesNotFound", service.ErrFuturesNotFound, codes.NotFound},
		{"ForexPairNotFound", service.ErrForexPairNotFound, codes.NotFound},
		{"OptionNotFound", service.ErrOptionNotFound, codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("Op: %w", tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Fatalf("errors.Is should match")
			}
			if got := status.Code(wrapped); got != tc.code {
				t.Errorf("expected %v, got %v", tc.code, got)
			}
		})
	}
}

func TestToExchangeProto_PopulatesAllFields(t *testing.T) {
	ex := &model.StockExchange{
		ID:              7,
		Name:            "New York Stock Exchange",
		Acronym:         "NYSE",
		MICCode:         "XNYS",
		Polity:          "USA",
		Currency:        "USD",
		TimeZone:        "America/New_York",
		OpenTime:        "09:30",
		CloseTime:       "16:00",
		PreMarketOpen:   "07:00",
		PostMarketClose: "20:00",
	}
	p := toExchangeProto(ex)
	if p.Id != 7 {
		t.Errorf("Id: %d", p.Id)
	}
	if p.Acronym != "NYSE" {
		t.Errorf("Acronym: %q", p.Acronym)
	}
	if p.MicCode != "XNYS" {
		t.Errorf("MicCode: %q", p.MicCode)
	}
	if p.PreMarketOpen != "07:00" {
		t.Errorf("PreMarketOpen: %q", p.PreMarketOpen)
	}
	if p.Currency != "USD" {
		t.Errorf("Currency: %q", p.Currency)
	}
}
