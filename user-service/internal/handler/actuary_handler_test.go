package handler

import (
	"context"
	"errors"
	"testing"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
)

// ---------------------------------------------------------------------------
// Mock
// ---------------------------------------------------------------------------

type mockActuarySvc struct {
	listActuariesFn      func(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error)
	getActuaryInfoFn     func(employeeID int64) (*model.ActuaryLimit, *model.Employee, error)
	setActuaryLimitFn    func(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error)
	resetUsedLimitFn     func(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error)
	setNeedApprovalFn    func(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error)
	updateUsedLimitFn    func(ctx context.Context, id int64, amount decimal.Decimal) (*model.ActuaryLimit, error)
}

func (m *mockActuarySvc) ListActuaries(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error) {
	if m.listActuariesFn != nil {
		return m.listActuariesFn(search, position, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockActuarySvc) GetActuaryInfo(employeeID int64) (*model.ActuaryLimit, *model.Employee, error) {
	if m.getActuaryInfoFn != nil {
		return m.getActuaryInfoFn(employeeID)
	}
	return nil, nil, nil
}

func (m *mockActuarySvc) SetActuaryLimit(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error) {
	if m.setActuaryLimitFn != nil {
		return m.setActuaryLimitFn(ctx, employeeID, limitAmount, changedBy)
	}
	return &model.ActuaryLimit{EmployeeID: employeeID, Limit: limitAmount}, nil
}

func (m *mockActuarySvc) ResetUsedLimit(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error) {
	if m.resetUsedLimitFn != nil {
		return m.resetUsedLimitFn(ctx, employeeID, changedBy)
	}
	return &model.ActuaryLimit{EmployeeID: employeeID}, nil
}

func (m *mockActuarySvc) SetNeedApproval(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error) {
	if m.setNeedApprovalFn != nil {
		return m.setNeedApprovalFn(ctx, employeeID, needApproval, changedBy)
	}
	return &model.ActuaryLimit{EmployeeID: employeeID, NeedApproval: needApproval}, nil
}

func (m *mockActuarySvc) UpdateUsedLimit(ctx context.Context, id int64, amount decimal.Decimal) (*model.ActuaryLimit, error) {
	if m.updateUsedLimitFn != nil {
		return m.updateUsedLimitFn(ctx, id, amount)
	}
	return &model.ActuaryLimit{ID: id}, nil
}

// ---------------------------------------------------------------------------
// ListActuaries
// ---------------------------------------------------------------------------

func TestListActuaries_Success(t *testing.T) {
	limitID := int64(10)
	h := newActuaryHandlerForTest(&mockActuarySvc{
		listActuariesFn: func(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error) {
			return []model.ActuaryRow{
				{EmployeeID: 1, FirstName: "Ana", LastName: "Jovic", Position: "agent", ActuaryLimitIDPtr: &limitID},
				{EmployeeID: 2, FirstName: "Marko", LastName: "Petrovic", Position: "supervisor", ActuaryLimitIDPtr: &limitID},
			}, 2, nil
		},
	})

	resp, err := h.ListActuaries(context.Background(), &pb.ListActuariesRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.TotalCount != 2 {
		t.Errorf("expected TotalCount=2, got %d", resp.TotalCount)
	}
	if len(resp.Actuaries) != 2 {
		t.Errorf("expected 2 actuaries, got %d", len(resp.Actuaries))
	}
	// supervisor position should map to role=supervisor
	if resp.Actuaries[1].Role != "supervisor" {
		t.Errorf("expected role=supervisor for position=supervisor, got %s", resp.Actuaries[1].Role)
	}
	// agent position should map to role=agent
	if resp.Actuaries[0].Role != "agent" {
		t.Errorf("expected role=agent, got %s", resp.Actuaries[0].Role)
	}
}

func TestListActuaries_ServiceError(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		listActuariesFn: func(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error) {
			return nil, 0, errors.New("db down")
		},
	})

	_, err := h.ListActuaries(context.Background(), &pb.ListActuariesRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// GetActuaryInfo
// ---------------------------------------------------------------------------

func TestGetActuaryInfo_Success(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		getActuaryInfoFn: func(employeeID int64) (*model.ActuaryLimit, *model.Employee, error) {
			return &model.ActuaryLimit{
					ID:           5,
					EmployeeID:   employeeID,
					Limit:        decimal.NewFromInt(5_000_000),
					UsedLimit:    decimal.NewFromInt(100_000),
					NeedApproval: true,
				}, &model.Employee{
					ID:        employeeID,
					FirstName: "Ana",
					LastName:  "Jovic",
					Email:     "ana@bank.rs",
					Roles:     []model.Role{{ID: 1, Name: "EmployeeAgent"}},
				}, nil
		},
	})

	resp, err := h.GetActuaryInfo(context.Background(), &pb.GetActuaryInfoRequest{EmployeeId: 3})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.EmployeeId != 3 {
		t.Errorf("expected EmployeeId=3, got %d", resp.EmployeeId)
	}
	if resp.FirstName != "Ana" {
		t.Errorf("expected FirstName=Ana, got %s", resp.FirstName)
	}
	if resp.Role != "agent" {
		t.Errorf("expected role=agent, got %s", resp.Role)
	}
	if !resp.NeedApproval {
		t.Error("expected NeedApproval=true")
	}
}

func TestGetActuaryInfo_SupervisorRole(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		getActuaryInfoFn: func(employeeID int64) (*model.ActuaryLimit, *model.Employee, error) {
			return &model.ActuaryLimit{ID: 6, EmployeeID: employeeID},
				&model.Employee{
					ID:    employeeID,
					Roles: []model.Role{{ID: 2, Name: "EmployeeSupervisor"}},
				}, nil
		},
	})

	resp, err := h.GetActuaryInfo(context.Background(), &pb.GetActuaryInfoRequest{EmployeeId: 4})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Role != "supervisor" {
		t.Errorf("expected role=supervisor, got %s", resp.Role)
	}
}

func TestGetActuaryInfo_ServiceError(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		getActuaryInfoFn: func(employeeID int64) (*model.ActuaryLimit, *model.Employee, error) {
			return nil, nil, errors.New("employee not found")
		},
	})

	_, err := h.GetActuaryInfo(context.Background(), &pb.GetActuaryInfoRequest{EmployeeId: 99})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SetActuaryLimit
// ---------------------------------------------------------------------------

func TestSetActuaryLimit_Success(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		setActuaryLimitFn: func(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error) {
			return &model.ActuaryLimit{ID: 7, EmployeeID: employeeID, Limit: limitAmount}, nil
		},
	})

	resp, err := h.SetActuaryLimit(context.Background(), &pb.SetActuaryLimitRequest{
		Id:    5,
		Limit: "3000000",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 7 {
		t.Errorf("expected ID=7, got %d", resp.Id)
	}
	if resp.Limit != "3000000" {
		t.Errorf("expected Limit=3000000, got %s", resp.Limit)
	}
}

func TestSetActuaryLimit_InvalidDecimal(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{})

	_, err := h.SetActuaryLimit(context.Background(), &pb.SetActuaryLimitRequest{
		Id:    5,
		Limit: "not-a-number",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", got)
	}
}

func TestSetActuaryLimit_ServiceError(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		setActuaryLimitFn: func(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error) {
			return nil, errors.New("db down")
		},
	})

	_, err := h.SetActuaryLimit(context.Background(), &pb.SetActuaryLimitRequest{Id: 5, Limit: "1000"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ResetActuaryUsedLimit
// ---------------------------------------------------------------------------

func TestResetActuaryUsedLimit_Success(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		resetUsedLimitFn: func(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error) {
			return &model.ActuaryLimit{ID: 8, EmployeeID: employeeID, UsedLimit: decimal.Zero}, nil
		},
	})

	resp, err := h.ResetActuaryUsedLimit(context.Background(), &pb.ResetActuaryUsedLimitRequest{Id: 8})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 8 {
		t.Errorf("expected ID=8, got %d", resp.Id)
	}
	if resp.UsedLimit != "0" {
		t.Errorf("expected UsedLimit=0, got %s", resp.UsedLimit)
	}
}

func TestResetActuaryUsedLimit_ServiceError(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		resetUsedLimitFn: func(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error) {
			return nil, errors.New("db down")
		},
	})

	_, err := h.ResetActuaryUsedLimit(context.Background(), &pb.ResetActuaryUsedLimitRequest{Id: 8})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SetNeedApproval
// ---------------------------------------------------------------------------

func TestSetNeedApproval_Success(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		setNeedApprovalFn: func(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error) {
			return &model.ActuaryLimit{ID: 9, EmployeeID: employeeID, NeedApproval: needApproval}, nil
		},
	})

	resp, err := h.SetNeedApproval(context.Background(), &pb.SetNeedApprovalRequest{Id: 9, NeedApproval: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 9 {
		t.Errorf("expected ID=9, got %d", resp.Id)
	}
	if !resp.NeedApproval {
		t.Error("expected NeedApproval=true")
	}
}

func TestSetNeedApproval_ServiceError(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		setNeedApprovalFn: func(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error) {
			return nil, errors.New("permission denied for action")
		},
	})

	_, err := h.SetNeedApproval(context.Background(), &pb.SetNeedApprovalRequest{Id: 9, NeedApproval: false})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.PermissionDenied {
		t.Errorf("expected codes.PermissionDenied, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// UpdateUsedLimit
// ---------------------------------------------------------------------------

func TestUpdateUsedLimit_Success(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		updateUsedLimitFn: func(ctx context.Context, id int64, amount decimal.Decimal) (*model.ActuaryLimit, error) {
			return &model.ActuaryLimit{ID: id, UsedLimit: amount}, nil
		},
	})

	resp, err := h.UpdateUsedLimit(context.Background(), &pb.UpdateUsedLimitRequest{Id: 11, Amount: "500000"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 11 {
		t.Errorf("expected ID=11, got %d", resp.Id)
	}
	if resp.UsedLimit != "500000" {
		t.Errorf("expected UsedLimit=500000, got %s", resp.UsedLimit)
	}
}

func TestUpdateUsedLimit_InvalidDecimal(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{})

	_, err := h.UpdateUsedLimit(context.Background(), &pb.UpdateUsedLimitRequest{Id: 11, Amount: "bad"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", got)
	}
}

func TestUpdateUsedLimit_ServiceError(t *testing.T) {
	h := newActuaryHandlerForTest(&mockActuarySvc{
		updateUsedLimitFn: func(ctx context.Context, id int64, amount decimal.Decimal) (*model.ActuaryLimit, error) {
			return nil, errors.New("not found")
		},
	})

	_, err := h.UpdateUsedLimit(context.Background(), &pb.UpdateUsedLimitRequest{Id: 11, Amount: "1000"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}
