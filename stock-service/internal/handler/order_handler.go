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

// orderSvcFacade is the narrow interface of OrderService used by OrderHandler.
type orderSvcFacade interface {
	CreateOrder(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error)
	GetOrder(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, []model.OrderTransaction, error)
	ListMyOrders(ownerType model.OwnerType, ownerID *uint64, filter repository.OrderFilter) ([]model.Order, int64, error)
	CancelOrder(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, error)
	ListAllOrders(filter repository.OrderFilter) ([]model.Order, int64, error)
	ApproveOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
	DeclineOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
}

// execEngineFacade is the narrow interface of OrderExecutionEngine used by OrderHandler.
type execEngineFacade interface {
	StartOrderExecution(ctx context.Context, orderID uint64)
	StopOrderExecution(orderID uint64)
}

type OrderHandler struct {
	pb.UnimplementedOrderGRPCServiceServer
	orderSvc   orderSvcFacade
	execEngine execEngineFacade
}

func NewOrderHandler(orderSvc *service.OrderService, execEngine *service.OrderExecutionEngine) *OrderHandler {
	return &OrderHandler{orderSvc: orderSvc, execEngine: execEngine}
}

// newOrderHandlerForTest constructs an OrderHandler with interface-typed
// dependencies for use in unit tests.
func newOrderHandlerForTest(orderSvc orderSvcFacade, execEngine execEngineFacade) *OrderHandler {
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

	targetUserID, targetSystemType, rerr := resolveOrderOwner(req.UserId, req.SystemType, req.ActingEmployeeId, req.OnBehalfOfClientId)
	if rerr != nil {
		return nil, rerr
	}
	actingEmployeeID := req.ActingEmployeeId

	// BaseAccountId on the proto is an optional uint64 populated by the
	// gateway for forex buy orders. Forward it to the service layer; the
	// service's forex gating (order_service.go CreateOrder) rejects forex
	// orders without it.
	var baseAccountID *uint64
	if req.BaseAccountId != nil {
		v := *req.BaseAccountId
		baseAccountID = &v
	}

	order, err := h.orderSvc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID:           targetUserID,
		SystemType:       targetSystemType,
		ListingID:        req.ListingId,
		HoldingID:        holdingID,
		Direction:        req.Direction,
		OrderType:        req.OrderType,
		Quantity:         req.Quantity,
		LimitValue:       limitVal,
		StopValue:        stopVal,
		AllOrNone:        req.AllOrNone,
		Margin:           req.Margin,
		AccountID:        req.AccountId,
		ActingEmployeeID: actingEmployeeID,
		BaseAccountID:    baseAccountID,
		OnBehalfOfFundID: req.OnBehalfOfFundId,
	})
	if err != nil {
		return nil, mapOrderError(err)
	}

	// Start execution for any approved order (placement saga now always
	// flips to "approved" on success).
	if order.Status == "approved" {
		h.execEngine.StartOrderExecution(ctx, order.ID)
	}

	return toOrderProto(order), nil
}

func (h *OrderHandler) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.OrderDetail, error) {
	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	order, txns, err := h.orderSvc.GetOrder(req.Id, ownerType, ownerID)
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
	listOwnerType, listOwnerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	orders, total, err := h.orderSvc.ListMyOrders(listOwnerType, listOwnerID, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toOrderListResponse(orders, total), nil
}

func (h *OrderHandler) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.Order, error) {
	cancelOwnerType, cancelOwnerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	order, err := h.orderSvc.CancelOrder(req.Id, cancelOwnerType, cancelOwnerID)
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
//
// mapOrderError is now a passthrough. Service-layer sentinels carry their own
// gRPC code via svcerr.SentinelError; placement-saga errors that are already
// gRPC status errors propagate verbatim. Bare errors that escape this filter
// will surface as codes.Unknown — fix at the source by wrapping with a
// sentinel from internal/service/errors.go.
func mapOrderError(err error) error {
	return err
}

// computeOrderState returns the computed "state" field from (status, is_done,
// has_fills). Users were confused that orders stayed in status="approved"
// after fully filling — state collapses (status, is_done, fill count) into a
// single label.
//
//	pending   → status=pending
//	declined  → status=declined
//	cancelled → status=cancelled
//	filled    → status=approved AND is_done
//	filling   → status=approved AND has fills but not done
//	approved  → status=approved AND no fills yet
func computeOrderState(o *model.Order, hasFills bool) string {
	switch o.Status {
	case "pending":
		return "pending"
	case "declined":
		return "declined"
	case "cancelled":
		return "cancelled"
	case "approved":
		if o.IsDone {
			return "filled"
		}
		if hasFills {
			return "filling"
		}
		return "approved"
	}
	return "unknown"
}

func toOrderProto(o *model.Order) *pb.Order {
	// has-fills is derived from RemainingPortions < Quantity. The execution
	// engine maintains this invariant on every partial fill.
	hasFills := o.RemainingPortions < o.Quantity
	filledQty := o.Quantity - o.RemainingPortions
	order := &pb.Order{
		Id:                o.ID,
		UserId:            model.OwnerIDOrZero(o.OwnerID),
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
		State:             computeOrderState(o, hasFills),
		FilledQuantity:    filledQty,
		ApprovedBy:        o.ApprovedBy,
		IsDone:            o.IsDone,
		RemainingPortions: o.RemainingPortions,
		AfterHours:        o.AfterHours,
		AllOrNone:         o.AllOrNone,
		Margin:            o.Margin,
		AccountId:         o.AccountID,
		ActingEmployeeId:  derefU64(o.ActingEmployeeID),
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
	// Refine the computed "state" field with the authoritative txn count —
	// toOrderProto has to guess from RemainingPortions; the detail endpoint
	// knows for sure.
	orderProto.State = computeOrderState(o, len(txns) > 0)
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

// derefU64 returns 0 when p is nil, otherwise *p. Used to bridge model fields
// that became *uint64 (e.g. Order.ActingEmployeeID) to proto fields whose
// schema is still uint64.
func derefU64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

// resolveOrderOwner decides who the order (and any resulting holding) belongs
// to. When an employee places on behalf of a client, the order is attributed
// to the client — both the user ID and the system_type flip — so the holding
// shows up in the client's /me/portfolio. The acting employee is recorded
// separately on the order for audit; this function does not touch it.
func resolveOrderOwner(reqUserID uint64, reqSystemType string, actingEmployeeID, onBehalfOfClientID uint64) (uint64, string, error) {
	if actingEmployeeID != 0 {
		if onBehalfOfClientID == 0 {
			return 0, "", status.Error(codes.InvalidArgument, "on_behalf_of_client_id required when acting_employee_id is set")
		}
		return onBehalfOfClientID, "client", nil
	}
	return reqUserID, reqSystemType, nil
}
