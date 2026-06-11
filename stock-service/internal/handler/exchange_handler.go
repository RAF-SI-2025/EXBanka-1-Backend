package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// exchangeSvcFacade is the narrow interface of ExchangeService used by ExchangeGRPCHandler.
type exchangeSvcFacade interface {
	ListExchanges(search string, page, pageSize int) ([]model.StockExchange, int64, error)
	GetExchange(id uint64) (*model.StockExchange, error)
	SetTestingMode(enabled bool) error
	GetTestingMode() bool
	// IsOpenForModel reports whether an already-loaded exchange is currently
	// open (testing mode OR within trading hours) without a per-row DB lookup.
	IsOpenForModel(ex *model.StockExchange) bool
}

// orderWaker lets enabling testing mode immediately wake the order-execution
// engine so every queued order (old and new) fills at once instead of waiting
// on its per-order timer. Optional — nil leaves the toggle a pure setting write.
type orderWaker interface {
	WakeAll()
}

type ExchangeGRPCHandler struct {
	pb.UnimplementedStockExchangeGRPCServiceServer
	svc   exchangeSvcFacade
	waker orderWaker
}

func NewExchangeGRPCHandler(svc *service.ExchangeService) *ExchangeGRPCHandler {
	return &ExchangeGRPCHandler{svc: svc}
}

// WithWaker wires the order-execution engine so SetTestingMode(true) immediately
// drives all queued orders to fill. Returns the handler for chaining.
func (h *ExchangeGRPCHandler) WithWaker(w orderWaker) *ExchangeGRPCHandler {
	h.waker = w
	return h
}

// newExchangeHandlerForTest constructs an ExchangeGRPCHandler with an
// interface-typed dependency for use in unit tests.
func newExchangeHandlerForTest(svc exchangeSvcFacade) *ExchangeGRPCHandler {
	return &ExchangeGRPCHandler{svc: svc}
}

func (h *ExchangeGRPCHandler) ListExchanges(ctx context.Context, req *pb.ListExchangesRequest) (*pb.ListExchangesResponse, error) {
	exchanges, total, err := h.svc.ListExchanges(req.Search, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, err
	}

	resp := &pb.ListExchangesResponse{TotalCount: total, Exchanges: make([]*pb.Exchange, 0, len(exchanges))}
	for i := range exchanges {
		ex := &exchanges[i]
		resp.Exchanges = append(resp.Exchanges, toExchangeProto(ex, h.svc.IsOpenForModel(ex)))
	}
	return resp, nil
}

func (h *ExchangeGRPCHandler) GetExchange(ctx context.Context, req *pb.GetExchangeRequest) (*pb.Exchange, error) {
	ex, err := h.svc.GetExchange(req.Id)
	if err != nil {
		return nil, err
	}
	return toExchangeProto(ex, h.svc.IsOpenForModel(ex)), nil
}

func (h *ExchangeGRPCHandler) SetTestingMode(ctx context.Context, req *pb.SetTestingModeRequest) (*pb.SetTestingModeResponse, error) {
	if err := h.svc.SetTestingMode(req.Enabled); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set testing mode: %v", err)
	}
	// Enabling testing mode fills every queued order (old and new) immediately:
	// wake all in-flight order goroutines so they re-evaluate now rather than
	// finishing their timers.
	if req.Enabled && h.waker != nil {
		h.waker.WakeAll()
	}
	return &pb.SetTestingModeResponse{TestingMode: req.Enabled}, nil
}

func (h *ExchangeGRPCHandler) GetTestingMode(ctx context.Context, req *pb.GetTestingModeRequest) (*pb.GetTestingModeResponse, error) {
	enabled := h.svc.GetTestingMode()
	return &pb.GetTestingModeResponse{TestingMode: enabled}, nil
}

func toExchangeProto(ex *model.StockExchange, isOpen bool) *pb.Exchange {
	return &pb.Exchange{
		Id:              ex.ID,
		Name:            ex.Name,
		Acronym:         ex.Acronym,
		MicCode:         ex.MICCode,
		Polity:          ex.Polity,
		Currency:        ex.Currency,
		TimeZone:        ex.TimeZone,
		OpenTime:        ex.OpenTime,
		CloseTime:       ex.CloseTime,
		PreMarketOpen:   ex.PreMarketOpen,
		PostMarketClose: ex.PostMarketClose,
		IsOpen:          isOpen,
	}
}
