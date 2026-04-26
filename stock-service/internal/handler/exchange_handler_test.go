package handler

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
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
	svc := &mockExchangeSvc{
		listFn: func(_ string, _, _ int) ([]model.StockExchange, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newExchangeHandlerForTest(svc)
	_, err := h.ListExchanges(context.Background(), &pb.ListExchangesRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
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
			return nil, errors.New("exchange not found")
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

func TestMapServiceError_NotFound(t *testing.T) {
	if got := mapServiceError(errors.New("exchange not found")); got != codes.NotFound {
		t.Errorf("expected NotFound, got %v", got)
	}
}

func TestMapServiceError_InvalidArgument(t *testing.T) {
	for _, msg := range []string{
		"value must be positive",
		"invalid input",
		"name must not be empty",
		"field is required",
	} {
		if got := mapServiceError(errors.New(msg)); got != codes.InvalidArgument {
			t.Errorf("%q: expected InvalidArgument, got %v", msg, got)
		}
	}
}

func TestMapServiceError_AlreadyExists(t *testing.T) {
	for _, msg := range []string{"record already exists", "duplicate entry"} {
		if got := mapServiceError(errors.New(msg)); got != codes.AlreadyExists {
			t.Errorf("%q: expected AlreadyExists, got %v", msg, got)
		}
	}
}

func TestMapServiceError_FailedPrecondition(t *testing.T) {
	for _, msg := range []string{
		"insufficient funds",
		"limit exceeded",
		"not enough shares",
	} {
		if got := mapServiceError(errors.New(msg)); got != codes.FailedPrecondition {
			t.Errorf("%q: expected FailedPrecondition, got %v", msg, got)
		}
	}
}

func TestMapServiceError_PermissionDenied(t *testing.T) {
	for _, msg := range []string{"permission denied", "forbidden access"} {
		if got := mapServiceError(errors.New(msg)); got != codes.PermissionDenied {
			t.Errorf("%q: expected PermissionDenied, got %v", msg, got)
		}
	}
}

func TestMapServiceError_DefaultInternal(t *testing.T) {
	if got := mapServiceError(errors.New("random db blowup")); got != codes.Internal {
		t.Errorf("expected Internal, got %v", got)
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
