package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

type OrderHandler struct {
	pb.UnimplementedOrderGRPCServiceServer
	orderSvc   *service.OrderService
	execEngine *service.OrderExecutionEngine
}

func NewOrderHandler(orderSvc *service.OrderService, execEngine *service.OrderExecutionEngine) *OrderHandler {
	return &OrderHandler{orderSvc: orderSvc, execEngine: execEngine}
}

func (h *OrderHandler) CreateOrder(ctx context.Context, req *pb.CreateOrderRequest) (*pb.Order, error) {
	var limitVal *decimal.Decimal
	if req.LimitValue != nil {
		v, err := decimal.NewFromString(*req.LimitValue)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid limit_value")
		}
		limitVal = &v
	}

	var stopVal *decimal.Decimal
	if req.StopValue != nil {
		v, err := decimal.NewFromString(*req.StopValue)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid stop_value")
		}
		stopVal = &v
	}

	var holdingID *uint64
	if req.HoldingId > 0 {
		v := req.HoldingId
		holdingID = &v
	}

	// Resolve effective user ID: when an employee places on behalf of a client,
	// the order belongs to the client and the employee is recorded for audit.
	targetUserID := req.UserId
	actingEmployeeID := req.ActingEmployeeId
	if req.ActingEmployeeId != 0 {
		if req.OnBehalfOfClientId == 0 {
			return nil, status.Error(codes.InvalidArgument, "on_behalf_of_client_id required when acting_employee_id is set")
		}
		targetUserID = req.OnBehalfOfClientId
	}

	order, err := h.orderSvc.CreateOrder(
		targetUserID, req.SystemType, req.ListingId, holdingID,
		req.Direction, req.OrderType, req.Quantity,
		limitVal, stopVal, req.AllOrNone, req.Margin, req.AccountId,
		actingEmployeeID,
	)
	if err != nil {
		return nil, mapOrderError(err)
	}

	// If auto-approved, start execution
	if order.Status == "approved" {
		h.execEngine.StartOrderExecution(ctx, order.ID)
	}

	return toOrderProto(order), nil
}

func (h *OrderHandler) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.OrderDetail, error) {
	order, txns, err := h.orderSvc.GetOrder(req.Id, req.UserId)
	if err != nil {
		return nil, mapOrderError(err)
	}
	return toOrderDetailProto(order, txns), nil
}

func (h *OrderHandler) ListMyOrders(ctx context.Context, req *pb.ListMyOrdersRequest) (*pb.ListOrdersResponse, error) {
	filter := repository.OrderFilter{
		Status:    req.Status,
		Direction: req.Direction,
		OrderType: req.OrderType,
		Page:      int(req.Page),
		PageSize:  int(req.PageSize),
	}
	orders, total, err := h.orderSvc.ListMyOrders(req.UserId, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toOrderListResponse(orders, total), nil
}

func (h *OrderHandler) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.CancelOrder(req.Id, req.UserId)
	if err != nil {
		return nil, mapOrderError(err)
	}
	h.execEngine.StopOrderExecution(order.ID)
	return toOrderProto(order), nil
}

func (h *OrderHandler) ListOrders(ctx context.Context, req *pb.ListOrdersRequest) (*pb.ListOrdersResponse, error) {
	filter := repository.OrderFilter{
		Status:     req.Status,
		AgentEmail: req.AgentEmail,
		Direction:  req.Direction,
		OrderType:  req.OrderType,
		Page:       int(req.Page),
		PageSize:   int(req.PageSize),
	}
	orders, total, err := h.orderSvc.ListAllOrders(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toOrderListResponse(orders, total), nil
}

func (h *OrderHandler) ApproveOrder(ctx context.Context, req *pb.ApproveOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.ApproveOrder(req.Id, req.SupervisorId, "")
	if err != nil {
		return nil, mapOrderError(err)
	}
	// Start execution now that it's approved
	h.execEngine.StartOrderExecution(ctx, order.ID)
	return toOrderProto(order), nil
}

func (h *OrderHandler) DeclineOrder(ctx context.Context, req *pb.DeclineOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.DeclineOrder(req.Id, req.SupervisorId, "")
	if err != nil {
		return nil, mapOrderError(err)
	}
	return toOrderProto(order), nil
}

// --- Mapping helpers ---

func mapOrderError(err error) error {
	switch err.Error() {
	case "order not found", "listing not found":
		return status.Error(codes.NotFound, err.Error())
	case "order does not belong to user":
		return status.Error(codes.PermissionDenied, err.Error())
	case "order is not pending", "order is already completed",
		"order is already declined/cancelled",
		"cannot approve: settlement date has passed":
		return status.Error(codes.FailedPrecondition, err.Error())
	case "limit_value required for limit/stop_limit orders",
		"stop_value required for stop/stop_limit orders":
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func toOrderProto(o *model.Order) *pb.Order {
	order := &pb.Order{
		Id:                o.ID,
		UserId:            o.UserID,
		ListingId:         o.ListingID,
		SecurityType:      o.SecurityType,
		Ticker:            o.Ticker,
		Direction:         o.Direction,
		OrderType:         o.OrderType,
		Quantity:          o.Quantity,
		ContractSize:      o.ContractSize,
		PricePerUnit:      o.PricePerUnit.StringFixed(4),
		ApproximatePrice:  o.ApproximatePrice.StringFixed(4),
		Commission:        o.Commission.StringFixed(2),
		Status:            o.Status,
		ApprovedBy:        o.ApprovedBy,
		IsDone:            o.IsDone,
		RemainingPortions: o.RemainingPortions,
		AfterHours:        o.AfterHours,
		AllOrNone:         o.AllOrNone,
		Margin:            o.Margin,
		AccountId:         o.AccountID,
		ActingEmployeeId:  o.ActingEmployeeID,
		LastModification:  o.LastModification.Format("2006-01-02T15:04:05Z"),
		CreatedAt:         o.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if o.HoldingID != nil {
		order.HoldingId = *o.HoldingID
	}
	if o.LimitValue != nil {
		s := o.LimitValue.StringFixed(4)
		order.LimitValue = &s
	}
	if o.StopValue != nil {
		s := o.StopValue.StringFixed(4)
		order.StopValue = &s
	}
	return order
}

func toOrderDetailProto(o *model.Order, txns []model.OrderTransaction) *pb.OrderDetail {
	orderProto := toOrderProto(o)
	txnProtos := make([]*pb.OrderTransaction, len(txns))
	for i, t := range txns {
		txnProtos[i] = &pb.OrderTransaction{
			Id:           t.ID,
			Quantity:     t.Quantity,
			PricePerUnit: t.PricePerUnit.StringFixed(4),
			TotalPrice:   t.TotalPrice.StringFixed(4),
			ExecutedAt:   t.ExecutedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	return &pb.OrderDetail{Order: orderProto, Transactions: txnProtos}
}

func toOrderListResponse(orders []model.Order, total int64) *pb.ListOrdersResponse {
	protos := make([]*pb.Order, len(orders))
	for i, o := range orders {
		protos[i] = toOrderProto(&o)
	}
	return &pb.ListOrdersResponse{Orders: protos, TotalCount: total}
}
