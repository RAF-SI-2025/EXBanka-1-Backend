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
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// ---------------------------------------------------------------------------
// Interfaces covering only the handler-called surface
// ---------------------------------------------------------------------------

type orderSvcFacade interface {
	CreateOrder(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error)
	GetOrder(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error)
	ListMyOrders(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error)
	CancelOrder(orderID, userID uint64, systemType string) (*model.Order, error)
	ListAllOrders(filter repository.OrderFilter) ([]model.Order, int64, error)
	ApproveOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
	DeclineOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
}

type execEngineFacade interface {
	StartOrderExecution(ctx context.Context, orderID uint64)
	StopOrderExecution(orderID uint64)
}

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockOrderSvc struct {
	createFn     func(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error)
	getFn        func(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error)
	listMyFn     func(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error)
	cancelFn     func(orderID, userID uint64, systemType string) (*model.Order, error)
	listAllFn    func(filter repository.OrderFilter) ([]model.Order, int64, error)
	approveFn    func(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
	declineFn    func(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
}

func (m *mockOrderSvc) CreateOrder(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error) {
	if m.createFn != nil {
		return m.createFn(ctx, req)
	}
	return &model.Order{ID: 1, Status: "pending", CreatedAt: time.Now(), LastModification: time.Now()}, nil
}

func (m *mockOrderSvc) GetOrder(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error) {
	if m.getFn != nil {
		return m.getFn(orderID, userID, systemType)
	}
	return &model.Order{ID: orderID, Status: "pending", CreatedAt: time.Now(), LastModification: time.Now()}, nil, nil
}

func (m *mockOrderSvc) ListMyOrders(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error) {
	if m.listMyFn != nil {
		return m.listMyFn(userID, systemType, filter)
	}
	return nil, 0, nil
}

func (m *mockOrderSvc) CancelOrder(orderID, userID uint64, systemType string) (*model.Order, error) {
	if m.cancelFn != nil {
		return m.cancelFn(orderID, userID, systemType)
	}
	return &model.Order{ID: orderID, Status: "cancelled", CreatedAt: time.Now(), LastModification: time.Now()}, nil
}

func (m *mockOrderSvc) ListAllOrders(filter repository.OrderFilter) ([]model.Order, int64, error) {
	if m.listAllFn != nil {
		return m.listAllFn(filter)
	}
	return nil, 0, nil
}

func (m *mockOrderSvc) ApproveOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error) {
	if m.approveFn != nil {
		return m.approveFn(orderID, supervisorID, supervisorName)
	}
	return &model.Order{ID: orderID, Status: "approved", CreatedAt: time.Now(), LastModification: time.Now()}, nil
}

func (m *mockOrderSvc) DeclineOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error) {
	if m.declineFn != nil {
		return m.declineFn(orderID, supervisorID, supervisorName)
	}
	return &model.Order{ID: orderID, Status: "declined", CreatedAt: time.Now(), LastModification: time.Now()}, nil
}

type mockExecEngine struct {
	startCalled []uint64
	stopCalled  []uint64
}

func (m *mockExecEngine) StartOrderExecution(_ context.Context, orderID uint64) {
	m.startCalled = append(m.startCalled, orderID)
}

func (m *mockExecEngine) StopOrderExecution(orderID uint64) {
	m.stopCalled = append(m.stopCalled, orderID)
}

// ---------------------------------------------------------------------------
// Testable OrderHandler variant that accepts interfaces
// ---------------------------------------------------------------------------

type testOrderHandler struct {
	pb.UnimplementedOrderGRPCServiceServer
	orderSvc   orderSvcFacade
	execEngine execEngineFacade
}

func newOrderHandlerForTest(svc orderSvcFacade, eng execEngineFacade) *testOrderHandler {
	return &testOrderHandler{orderSvc: svc, execEngine: eng}
}

// Mirror the real handler's RPC methods — delegate to the interface.

func (h *testOrderHandler) CreateOrder(ctx context.Context, req *pb.CreateOrderRequest) (*pb.Order, error) {
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
		ActingEmployeeID: req.ActingEmployeeId,
		BaseAccountID:    baseAccountID,
	})
	if err != nil {
		return nil, mapOrderError(err)
	}
	if order.Status == "approved" {
		h.execEngine.StartOrderExecution(ctx, order.ID)
	}
	return toOrderProto(order), nil
}

func (h *testOrderHandler) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.OrderDetail, error) {
	order, txns, err := h.orderSvc.GetOrder(req.Id, req.UserId, req.SystemType)
	if err != nil {
		return nil, mapOrderError(err)
	}
	return toOrderDetailProto(order, txns), nil
}

func (h *testOrderHandler) ListMyOrders(ctx context.Context, req *pb.ListMyOrdersRequest) (*pb.ListOrdersResponse, error) {
	filter := repository.OrderFilter{
		Status:    req.Status,
		Direction: req.Direction,
		OrderType: req.OrderType,
		Page:      int(req.Page),
		PageSize:  int(req.PageSize),
	}
	orders, total, err := h.orderSvc.ListMyOrders(req.UserId, req.SystemType, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toOrderListResponse(orders, total), nil
}

func (h *testOrderHandler) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.CancelOrder(req.Id, req.UserId, req.SystemType)
	if err != nil {
		return nil, mapOrderError(err)
	}
	h.execEngine.StopOrderExecution(order.ID)
	return toOrderProto(order), nil
}

func (h *testOrderHandler) ListOrders(ctx context.Context, req *pb.ListOrdersRequest) (*pb.ListOrdersResponse, error) {
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

func (h *testOrderHandler) ApproveOrder(ctx context.Context, req *pb.ApproveOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.ApproveOrder(req.Id, req.SupervisorId, "")
	if err != nil {
		return nil, mapOrderError(err)
	}
	h.execEngine.StartOrderExecution(ctx, order.ID)
	return toOrderProto(order), nil
}

func (h *testOrderHandler) DeclineOrder(ctx context.Context, req *pb.DeclineOrderRequest) (*pb.Order, error) {
	order, err := h.orderSvc.DeclineOrder(req.Id, req.SupervisorId, "")
	if err != nil {
		return nil, mapOrderError(err)
	}
	return toOrderProto(order), nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestOrderHandler_CreateOrder_Success(t *testing.T) {
	svc := &mockOrderSvc{
		createFn: func(_ context.Context, req service.CreateOrderRequest) (*model.Order, error) {
			return &model.Order{
				ID:               99,
				UserID:           req.UserID,
				Status:           "pending",
				Direction:        req.Direction,
				OrderType:        req.OrderType,
				Quantity:         req.Quantity,
				PricePerUnit:     decimal.NewFromFloat(10),
				ApproximatePrice: decimal.NewFromFloat(10),
				Commission:       decimal.NewFromFloat(0.5),
				CreatedAt:        time.Now(),
				LastModification: time.Now(),
			}, nil
		},
	}
	eng := &mockExecEngine{}
	h := newOrderHandlerForTest(svc, eng)

	resp, err := h.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		UserId:     42,
		SystemType: "client",
		ListingId:  1,
		Direction:  "buy",
		OrderType:  "market",
		Quantity:   10,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != 99 {
		t.Errorf("expected id=99, got %d", resp.Id)
	}
	if resp.Status != "pending" {
		t.Errorf("expected status=pending, got %q", resp.Status)
	}
	// pending order: exec engine must NOT be called
	if len(eng.startCalled) != 0 {
		t.Errorf("expected no StartOrderExecution calls, got %d", len(eng.startCalled))
	}
}

func TestOrderHandler_CreateOrder_Approved_StartsExecution(t *testing.T) {
	svc := &mockOrderSvc{
		createFn: func(_ context.Context, req service.CreateOrderRequest) (*model.Order, error) {
			return &model.Order{
				ID:               5,
				Status:           "approved",
				PricePerUnit:     decimal.NewFromFloat(10),
				ApproximatePrice: decimal.NewFromFloat(10),
				Commission:       decimal.Zero,
				CreatedAt:        time.Now(),
				LastModification: time.Now(),
			}, nil
		},
	}
	eng := &mockExecEngine{}
	h := newOrderHandlerForTest(svc, eng)

	_, err := h.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		UserId:     1,
		SystemType: "employee",
		ListingId:  2,
		Direction:  "buy",
		OrderType:  "market",
		Quantity:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eng.startCalled) != 1 || eng.startCalled[0] != 5 {
		t.Errorf("expected StartOrderExecution(5), got %v", eng.startCalled)
	}
}

func TestOrderHandler_CreateOrder_ServiceError(t *testing.T) {
	svc := &mockOrderSvc{
		createFn: func(_ context.Context, req service.CreateOrderRequest) (*model.Order, error) {
			return nil, errors.New("order not found")
		},
	}
	eng := &mockExecEngine{}
	h := newOrderHandlerForTest(svc, eng)

	_, err := h.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		UserId:     1,
		SystemType: "client",
		ListingId:  1,
		Direction:  "buy",
		OrderType:  "market",
		Quantity:   1,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestOrderHandler_CreateOrder_InvalidLimitValue(t *testing.T) {
	h := newOrderHandlerForTest(&mockOrderSvc{}, &mockExecEngine{})
	lv := "not-a-number"
	_, err := h.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		UserId:     1,
		SystemType: "client",
		LimitValue: &lv,
	})
	if err == nil {
		t.Fatal("expected error for invalid limit_value")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestOrderHandler_CreateOrder_MissingClientWhenActingAsEmployee(t *testing.T) {
	h := newOrderHandlerForTest(&mockOrderSvc{}, &mockExecEngine{})
	_, err := h.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		UserId:           1,
		SystemType:       "employee",
		ActingEmployeeId: 10,
		// OnBehalfOfClientId intentionally missing (0)
	})
	if err == nil {
		t.Fatal("expected error when on_behalf_of_client_id missing")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestOrderHandler_GetOrder_Success(t *testing.T) {
	svc := &mockOrderSvc{
		getFn: func(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error) {
			return &model.Order{
				ID:               orderID,
				Status:           "pending",
				PricePerUnit:     decimal.NewFromFloat(10),
				ApproximatePrice: decimal.NewFromFloat(10),
				Commission:       decimal.Zero,
				CreatedAt:        time.Now(),
				LastModification: time.Now(),
			}, nil, nil
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	resp, err := h.GetOrder(context.Background(), &pb.GetOrderRequest{Id: 7, UserId: 42, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Order.Id != 7 {
		t.Errorf("expected id=7, got %d", resp.Order.Id)
	}
}

func TestOrderHandler_GetOrder_NotFound(t *testing.T) {
	svc := &mockOrderSvc{
		getFn: func(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error) {
			return nil, nil, errors.New("order not found")
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	_, err := h.GetOrder(context.Background(), &pb.GetOrderRequest{Id: 99, UserId: 1, SystemType: "client"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestOrderHandler_ListMyOrders_Success(t *testing.T) {
	svc := &mockOrderSvc{
		listMyFn: func(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error) {
			return []model.Order{
				{
					ID:               1,
					Status:           "pending",
					PricePerUnit:     decimal.NewFromFloat(5),
					ApproximatePrice: decimal.NewFromFloat(5),
					Commission:       decimal.Zero,
					CreatedAt:        time.Now(),
					LastModification: time.Now(),
				},
			}, 1, nil
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	resp, err := h.ListMyOrders(context.Background(), &pb.ListMyOrdersRequest{UserId: 1, SystemType: "client", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("expected TotalCount=1, got %d", resp.TotalCount)
	}
	if len(resp.Orders) != 1 {
		t.Errorf("expected 1 order, got %d", len(resp.Orders))
	}
}

func TestOrderHandler_ListMyOrders_Error(t *testing.T) {
	svc := &mockOrderSvc{
		listMyFn: func(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error) {
			return nil, 0, errors.New("db error")
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	_, err := h.ListMyOrders(context.Background(), &pb.ListMyOrdersRequest{UserId: 1, SystemType: "client"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestOrderHandler_CancelOrder_Success(t *testing.T) {
	svc := &mockOrderSvc{
		cancelFn: func(orderID, userID uint64, systemType string) (*model.Order, error) {
			return &model.Order{
				ID:               orderID,
				Status:           "cancelled",
				PricePerUnit:     decimal.NewFromFloat(10),
				ApproximatePrice: decimal.NewFromFloat(10),
				Commission:       decimal.Zero,
				CreatedAt:        time.Now(),
				LastModification: time.Now(),
			}, nil
		},
	}
	eng := &mockExecEngine{}
	h := newOrderHandlerForTest(svc, eng)

	resp, err := h.CancelOrder(context.Background(), &pb.CancelOrderRequest{Id: 3, UserId: 1, SystemType: "client"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "cancelled" {
		t.Errorf("expected cancelled, got %q", resp.Status)
	}
	if len(eng.stopCalled) != 1 || eng.stopCalled[0] != 3 {
		t.Errorf("expected StopOrderExecution(3), got %v", eng.stopCalled)
	}
}

func TestOrderHandler_CancelOrder_NotOwner(t *testing.T) {
	svc := &mockOrderSvc{
		cancelFn: func(orderID, userID uint64, systemType string) (*model.Order, error) {
			return nil, errors.New("order does not belong to user")
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	_, err := h.CancelOrder(context.Background(), &pb.CancelOrderRequest{Id: 3, UserId: 2, SystemType: "client"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", status.Code(err))
	}
}

func TestOrderHandler_ListOrders_Success(t *testing.T) {
	svc := &mockOrderSvc{
		listAllFn: func(filter repository.OrderFilter) ([]model.Order, int64, error) {
			return []model.Order{
				{
					ID:               10,
					Status:           "pending",
					PricePerUnit:     decimal.NewFromFloat(5),
					ApproximatePrice: decimal.NewFromFloat(5),
					Commission:       decimal.Zero,
					CreatedAt:        time.Now(),
					LastModification: time.Now(),
				},
				{
					ID:               11,
					Status:           "approved",
					PricePerUnit:     decimal.NewFromFloat(20),
					ApproximatePrice: decimal.NewFromFloat(20),
					Commission:       decimal.Zero,
					CreatedAt:        time.Now(),
					LastModification: time.Now(),
				},
			}, 2, nil
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	resp, err := h.ListOrders(context.Background(), &pb.ListOrdersRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 2 {
		t.Errorf("expected TotalCount=2, got %d", resp.TotalCount)
	}
}

func TestOrderHandler_ListOrders_Error(t *testing.T) {
	svc := &mockOrderSvc{
		listAllFn: func(filter repository.OrderFilter) ([]model.Order, int64, error) {
			return nil, 0, errors.New("db failure")
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	_, err := h.ListOrders(context.Background(), &pb.ListOrdersRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestOrderHandler_ApproveOrder_Success(t *testing.T) {
	svc := &mockOrderSvc{
		approveFn: func(orderID, supervisorID uint64, supervisorName string) (*model.Order, error) {
			return &model.Order{
				ID:               orderID,
				Status:           "approved",
				PricePerUnit:     decimal.NewFromFloat(10),
				ApproximatePrice: decimal.NewFromFloat(10),
				Commission:       decimal.Zero,
				CreatedAt:        time.Now(),
				LastModification: time.Now(),
			}, nil
		},
	}
	eng := &mockExecEngine{}
	h := newOrderHandlerForTest(svc, eng)

	resp, err := h.ApproveOrder(context.Background(), &pb.ApproveOrderRequest{Id: 4, SupervisorId: 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "approved" {
		t.Errorf("expected approved, got %q", resp.Status)
	}
	if len(eng.startCalled) != 1 || eng.startCalled[0] != 4 {
		t.Errorf("expected StartOrderExecution(4), got %v", eng.startCalled)
	}
}

func TestOrderHandler_ApproveOrder_NotPending(t *testing.T) {
	svc := &mockOrderSvc{
		approveFn: func(orderID, supervisorID uint64, supervisorName string) (*model.Order, error) {
			return nil, errors.New("order is not pending")
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	_, err := h.ApproveOrder(context.Background(), &pb.ApproveOrderRequest{Id: 1, SupervisorId: 9})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestOrderHandler_DeclineOrder_Success(t *testing.T) {
	svc := &mockOrderSvc{
		declineFn: func(orderID, supervisorID uint64, supervisorName string) (*model.Order, error) {
			return &model.Order{
				ID:               orderID,
				Status:           "declined",
				PricePerUnit:     decimal.NewFromFloat(10),
				ApproximatePrice: decimal.NewFromFloat(10),
				Commission:       decimal.Zero,
				CreatedAt:        time.Now(),
				LastModification: time.Now(),
			}, nil
		},
	}
	eng := &mockExecEngine{}
	h := newOrderHandlerForTest(svc, eng)

	resp, err := h.DeclineOrder(context.Background(), &pb.DeclineOrderRequest{Id: 6, SupervisorId: 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "declined" {
		t.Errorf("expected declined, got %q", resp.Status)
	}
	// decline does not touch exec engine
	if len(eng.startCalled) != 0 {
		t.Errorf("expected no exec engine calls on decline, got %v", eng.startCalled)
	}
}

func TestOrderHandler_DeclineOrder_NotFound(t *testing.T) {
	svc := &mockOrderSvc{
		declineFn: func(orderID, supervisorID uint64, supervisorName string) (*model.Order, error) {
			return nil, errors.New("order not found")
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})
	_, err := h.DeclineOrder(context.Background(), &pb.DeclineOrderRequest{Id: 99, SupervisorId: 9})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}
