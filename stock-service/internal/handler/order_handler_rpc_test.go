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
// Mock implementations
// ---------------------------------------------------------------------------

type mockOrderSvc struct {
	createFn  func(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error)
	getFn     func(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, []model.OrderTransaction, error)
	listMyFn  func(ownerType model.OwnerType, ownerID *uint64, filter repository.OrderFilter) ([]model.Order, int64, error)
	cancelFn  func(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, error)
	listAllFn func(filter repository.OrderFilter) ([]model.Order, int64, error)
	approveFn func(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
	declineFn func(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error)
}

func (m *mockOrderSvc) CreateOrder(ctx context.Context, req service.CreateOrderRequest) (*model.Order, error) {
	if m.createFn != nil {
		return m.createFn(ctx, req)
	}
	return &model.Order{ID: 1, Status: "pending", CreatedAt: time.Now(), LastModification: time.Now()}, nil
}

func (m *mockOrderSvc) GetOrder(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, []model.OrderTransaction, error) {
	if m.getFn != nil {
		return m.getFn(orderID, ownerType, ownerID)
	}
	return &model.Order{ID: orderID, Status: "pending", CreatedAt: time.Now(), LastModification: time.Now()}, nil, nil
}

func (m *mockOrderSvc) ListMyOrders(ownerType model.OwnerType, ownerID *uint64, filter repository.OrderFilter) ([]model.Order, int64, error) {
	if m.listMyFn != nil {
		return m.listMyFn(ownerType, ownerID, filter)
	}
	return nil, 0, nil
}

func (m *mockOrderSvc) CancelOrder(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, error) {
	if m.cancelFn != nil {
		return m.cancelFn(orderID, ownerType, ownerID)
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
// Tests
// ---------------------------------------------------------------------------

func TestOrderHandler_CreateOrder_Success(t *testing.T) {
	svc := &mockOrderSvc{
		createFn: func(_ context.Context, req service.CreateOrderRequest) (*model.Order, error) {
			ot, oid := model.OwnerFromLegacy(req.UserID, req.SystemType)
			return &model.Order{
				ID:               99,
				OwnerType:        ot,
				OwnerID:          oid,
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
			return nil, fmt.Errorf("order not found: %w", service.ErrOrderNotFound)
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
		getFn: func(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, []model.OrderTransaction, error) {
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
		getFn: func(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, []model.OrderTransaction, error) {
			return nil, nil, fmt.Errorf("order not found: %w", service.ErrOrderNotFound)
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
		listMyFn: func(ownerType model.OwnerType, ownerID *uint64, filter repository.OrderFilter) ([]model.Order, int64, error) {
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
		listMyFn: func(ownerType model.OwnerType, ownerID *uint64, filter repository.OrderFilter) ([]model.Order, int64, error) {
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
		cancelFn: func(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, error) {
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
		cancelFn: func(orderID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.Order, error) {
			return nil, fmt.Errorf("order does not belong to user: %w", service.ErrOrderOwnership)
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
			return nil, fmt.Errorf("order is not pending: %w", service.ErrOrderNotPending)
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
			return nil, fmt.Errorf("order not found: %w", service.ErrOrderNotFound)
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
