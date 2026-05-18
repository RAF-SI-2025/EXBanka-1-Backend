package handler

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/service"
	pb "github.com/exbanka/contract/clientpb"
)

type mockClientLimitSvc struct {
	getFn func(clientID int64) (*model.ClientLimit, error)
	setFn func(ctx context.Context, limit model.ClientLimit, changedBy int64) (*model.ClientLimit, error)
}

func (m *mockClientLimitSvc) GetClientLimits(clientID int64) (*model.ClientLimit, error) {
	if m.getFn != nil {
		return m.getFn(clientID)
	}
	return &model.ClientLimit{ID: 1, ClientID: clientID}, nil
}

func (m *mockClientLimitSvc) SetClientLimits(ctx context.Context, limit model.ClientLimit, changedBy int64) (*model.ClientLimit, error) {
	if m.setFn != nil {
		return m.setFn(ctx, limit, changedBy)
	}
	return &limit, nil
}

func TestClientLimitHandler_GetClientLimits_Success(t *testing.T) {
	svc := &mockClientLimitSvc{
		getFn: func(clientID int64) (*model.ClientLimit, error) {
			return &model.ClientLimit{
				ID: 5, ClientID: clientID,
				DailyLimit:    decimal.NewFromInt(50000),
				MonthlyLimit:  decimal.NewFromInt(500000),
				TransferLimit: decimal.NewFromInt(100000),
				SetByEmployee: 7,
			}, nil
		},
	}
	h := newClientLimitGRPCHandlerForTest(svc)
	resp, err := h.GetClientLimits(context.Background(), &pb.GetClientLimitRequest{ClientId: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ClientId != 5 || resp.DailyLimit != "50000" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestClientLimitHandler_GetClientLimits_NotFound(t *testing.T) {
	svc := &mockClientLimitSvc{
		getFn: func(_ int64) (*model.ClientLimit, error) {
			return nil, service.ErrClientNotFound
		},
	}
	h := newClientLimitGRPCHandlerForTest(svc)
	_, err := h.GetClientLimits(context.Background(), &pb.GetClientLimitRequest{ClientId: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestClientLimitHandler_SetClientLimits_Success(t *testing.T) {
	svc := &mockClientLimitSvc{
		setFn: func(_ context.Context, limit model.ClientLimit, _ int64) (*model.ClientLimit, error) {
			limit.ID = 10
			return &limit, nil
		},
	}
	h := newClientLimitGRPCHandlerForTest(svc)
	resp, err := h.SetClientLimits(context.Background(), &pb.SetClientLimitRequest{
		ClientId: 5, DailyLimit: "50000", MonthlyLimit: "500000", TransferLimit: "100000", SetByEmployee: 7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != 10 || resp.SetByEmployee != 7 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestClientLimitHandler_SetClientLimits_InvalidDaily(t *testing.T) {
	h := newClientLimitGRPCHandlerForTest(&mockClientLimitSvc{})
	_, err := h.SetClientLimits(context.Background(), &pb.SetClientLimitRequest{
		ClientId: 5, DailyLimit: "not-a-number", MonthlyLimit: "100", TransferLimit: "100",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestClientLimitHandler_SetClientLimits_InvalidMonthly(t *testing.T) {
	h := newClientLimitGRPCHandlerForTest(&mockClientLimitSvc{})
	_, err := h.SetClientLimits(context.Background(), &pb.SetClientLimitRequest{
		ClientId: 5, DailyLimit: "100", MonthlyLimit: "bad", TransferLimit: "100",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestClientLimitHandler_SetClientLimits_InvalidTransfer(t *testing.T) {
	h := newClientLimitGRPCHandlerForTest(&mockClientLimitSvc{})
	_, err := h.SetClientLimits(context.Background(), &pb.SetClientLimitRequest{
		ClientId: 5, DailyLimit: "100", MonthlyLimit: "100", TransferLimit: "bad",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestClientLimitHandler_SetClientLimits_ServiceError(t *testing.T) {
	svc := &mockClientLimitSvc{
		setFn: func(_ context.Context, _ model.ClientLimit, _ int64) (*model.ClientLimit, error) {
			return nil, service.ErrLimitsExceedEmployee
		},
	}
	h := newClientLimitGRPCHandlerForTest(svc)
	_, err := h.SetClientLimits(context.Background(), &pb.SetClientLimitRequest{
		ClientId: 5, DailyLimit: "0", MonthlyLimit: "100", TransferLimit: "100",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}
