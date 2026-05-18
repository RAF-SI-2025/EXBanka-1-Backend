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
}

type ExchangeGRPCHandler struct {
	pb.UnimplementedStockExchangeGRPCServiceServer
	svc exchangeSvcFacade
}

func NewExchangeGRPCHandler(svc *service.ExchangeService) *ExchangeGRPCHandler {
	return &ExchangeGRPCHandler{svc: svc}
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
	for _, ex := range exchanges {
		resp.Exchanges = append(resp.Exchanges, toExchangeProto(&ex))
	}
	return resp, nil
}

func (h *ExchangeGRPCHandler) GetExchange(ctx context.Context, req *pb.GetExchangeRequest) (*pb.Exchange, error) {
	ex, err := h.svc.GetExchange(req.Id)
	if err != nil {
		return nil, err
	}
	return toExchangeProto(ex), nil
}

func (h *ExchangeGRPCHandler) SetTestingMode(ctx context.Context, req *pb.SetTestingModeRequest) (*pb.SetTestingModeResponse, error) {
	if err := h.svc.SetTestingMode(req.Enabled); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set testing mode: %v", err)
	}
	return &pb.SetTestingModeResponse{TestingMode: req.Enabled}, nil
}

func (h *ExchangeGRPCHandler) GetTestingMode(ctx context.Context, req *pb.GetTestingModeRequest) (*pb.GetTestingModeResponse, error) {
	enabled := h.svc.GetTestingMode()
	return &pb.GetTestingModeResponse{TestingMode: enabled}, nil
}

func toExchangeProto(ex *model.StockExchange) *pb.Exchange {
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
	}
}
