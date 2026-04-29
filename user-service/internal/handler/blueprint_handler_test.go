package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
	"google.golang.org/grpc/codes"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Mock
// ---------------------------------------------------------------------------

type mockBlueprintSvc struct {
	createBlueprintFn func(ctx context.Context, bp model.LimitBlueprint) (*model.LimitBlueprint, error)
	getBlueprintFn    func(id uint64) (*model.LimitBlueprint, error)
	listBlueprintsFn  func(bpType string) ([]model.LimitBlueprint, error)
	updateBlueprintFn func(ctx context.Context, id uint64, name, description string, values json.RawMessage) (*model.LimitBlueprint, error)
	deleteBlueprintFn func(ctx context.Context, id uint64) error
	applyBlueprintFn  func(ctx context.Context, blueprintID uint64, targetID int64, appliedBy int64) error
}

func (m *mockBlueprintSvc) CreateBlueprint(ctx context.Context, bp model.LimitBlueprint) (*model.LimitBlueprint, error) {
	if m.createBlueprintFn != nil {
		return m.createBlueprintFn(ctx, bp)
	}
	bp.ID = 1
	return &bp, nil
}

func (m *mockBlueprintSvc) GetBlueprint(id uint64) (*model.LimitBlueprint, error) {
	if m.getBlueprintFn != nil {
		return m.getBlueprintFn(id)
	}
	return nil, nil
}

func (m *mockBlueprintSvc) ListBlueprints(bpType string) ([]model.LimitBlueprint, error) {
	if m.listBlueprintsFn != nil {
		return m.listBlueprintsFn(bpType)
	}
	return nil, nil
}

func (m *mockBlueprintSvc) UpdateBlueprint(ctx context.Context, id uint64, name, description string, values json.RawMessage) (*model.LimitBlueprint, error) {
	if m.updateBlueprintFn != nil {
		return m.updateBlueprintFn(ctx, id, name, description, values)
	}
	return &model.LimitBlueprint{ID: id, Name: name, Description: description}, nil
}

func (m *mockBlueprintSvc) DeleteBlueprint(ctx context.Context, id uint64) error {
	if m.deleteBlueprintFn != nil {
		return m.deleteBlueprintFn(ctx, id)
	}
	return nil
}

func (m *mockBlueprintSvc) ApplyBlueprint(ctx context.Context, blueprintID uint64, targetID int64, appliedBy int64) error {
	if m.applyBlueprintFn != nil {
		return m.applyBlueprintFn(ctx, blueprintID, targetID, appliedBy)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func employeeBlueprintJSON() string {
	return `{"max_loan_approval_amount":"50000","max_single_transaction":"100000","max_daily_transaction":"500000","max_client_daily_limit":"250000","max_client_monthly_limit":"2500000"}`
}

func newEmployeeBlueprint(id uint64) *model.LimitBlueprint {
	return &model.LimitBlueprint{
		ID:          id,
		Name:        "TestBlueprint",
		Description: "A test blueprint",
		Type:        model.BlueprintTypeEmployee,
		Values:      datatypes.JSON([]byte(employeeBlueprintJSON())),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

// ---------------------------------------------------------------------------
// CreateBlueprint
// ---------------------------------------------------------------------------

func TestCreateBlueprint_Success(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		createBlueprintFn: func(ctx context.Context, bp model.LimitBlueprint) (*model.LimitBlueprint, error) {
			bp.ID = 42
			bp.CreatedAt = time.Now()
			bp.UpdatedAt = time.Now()
			return &bp, nil
		},
	})

	resp, err := h.CreateBlueprint(context.Background(), &pb.CreateBlueprintRequest{
		Name:        "TestBlueprint",
		Description: "A test blueprint",
		Type:        model.BlueprintTypeEmployee,
		ValuesJson:  employeeBlueprintJSON(),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 42 {
		t.Errorf("expected ID=42, got %d", resp.Id)
	}
	if resp.Name != "TestBlueprint" {
		t.Errorf("expected Name=TestBlueprint, got %s", resp.Name)
	}
	if resp.Type != model.BlueprintTypeEmployee {
		t.Errorf("expected Type=employee, got %s", resp.Type)
	}
}

func TestCreateBlueprint_ServiceError(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		createBlueprintFn: func(ctx context.Context, bp model.LimitBlueprint) (*model.LimitBlueprint, error) {
			return nil, service.ErrEmployeeAlreadyExists
		},
	})

	_, err := h.CreateBlueprint(context.Background(), &pb.CreateBlueprintRequest{
		Name:       "Dup",
		Type:       model.BlueprintTypeEmployee,
		ValuesJson: employeeBlueprintJSON(),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.AlreadyExists {
		t.Errorf("expected codes.AlreadyExists, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// GetBlueprint
// ---------------------------------------------------------------------------

func TestGetBlueprint_Success(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		getBlueprintFn: func(id uint64) (*model.LimitBlueprint, error) {
			return newEmployeeBlueprint(id), nil
		},
	})

	resp, err := h.GetBlueprint(context.Background(), &pb.GetBlueprintRequest{Id: 5})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 5 {
		t.Errorf("expected ID=5, got %d", resp.Id)
	}
	if resp.Type != model.BlueprintTypeEmployee {
		t.Errorf("expected type=employee, got %s", resp.Type)
	}
}

func TestGetBlueprint_NotFound(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		getBlueprintFn: func(id uint64) (*model.LimitBlueprint, error) {
			return nil, gorm.ErrRecordNotFound
		},
	})

	_, err := h.GetBlueprint(context.Background(), &pb.GetBlueprintRequest{Id: 999})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ListBlueprints
// ---------------------------------------------------------------------------

func TestListBlueprints_Success(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		listBlueprintsFn: func(bpType string) ([]model.LimitBlueprint, error) {
			return []model.LimitBlueprint{
				*newEmployeeBlueprint(1),
				*newEmployeeBlueprint(2),
			}, nil
		},
	})

	resp, err := h.ListBlueprints(context.Background(), &pb.ListBlueprintsRequest{Type: model.BlueprintTypeEmployee})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Blueprints) != 2 {
		t.Errorf("expected 2 blueprints, got %d", len(resp.Blueprints))
	}
}

func TestListBlueprints_ServiceError(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		listBlueprintsFn: func(bpType string) ([]model.LimitBlueprint, error) {
			return nil, errors.New("db down")
		},
	})

	_, err := h.ListBlueprints(context.Background(), &pb.ListBlueprintsRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// UpdateBlueprint
// ---------------------------------------------------------------------------

func TestUpdateBlueprint_Success(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		updateBlueprintFn: func(ctx context.Context, id uint64, name, description string, values json.RawMessage) (*model.LimitBlueprint, error) {
			bp := newEmployeeBlueprint(id)
			bp.Name = name
			bp.Description = description
			return bp, nil
		},
	})

	resp, err := h.UpdateBlueprint(context.Background(), &pb.UpdateBlueprintRequest{
		Id:          3,
		Name:        "Updated",
		Description: "Updated description",
		ValuesJson:  employeeBlueprintJSON(),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 3 {
		t.Errorf("expected ID=3, got %d", resp.Id)
	}
	if resp.Name != "Updated" {
		t.Errorf("expected Name=Updated, got %s", resp.Name)
	}
}

func TestUpdateBlueprint_ServiceError(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		updateBlueprintFn: func(ctx context.Context, id uint64, name, description string, values json.RawMessage) (*model.LimitBlueprint, error) {
			return nil, gorm.ErrRecordNotFound
		},
	})

	_, err := h.UpdateBlueprint(context.Background(), &pb.UpdateBlueprintRequest{Id: 999, Name: "X"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// DeleteBlueprint
// ---------------------------------------------------------------------------

func TestDeleteBlueprint_Success(t *testing.T) {
	deleted := false
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		deleteBlueprintFn: func(ctx context.Context, id uint64) error {
			deleted = true
			return nil
		},
	})

	resp, err := h.DeleteBlueprint(context.Background(), &pb.DeleteBlueprintRequest{Id: 7})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if !deleted {
		t.Error("expected DeleteBlueprint to be called")
	}
}

func TestDeleteBlueprint_ServiceError(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		deleteBlueprintFn: func(ctx context.Context, id uint64) error {
			return gorm.ErrRecordNotFound
		},
	})

	_, err := h.DeleteBlueprint(context.Background(), &pb.DeleteBlueprintRequest{Id: 999})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ApplyBlueprint
// ---------------------------------------------------------------------------

func TestApplyBlueprint_Success(t *testing.T) {
	applied := false
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		applyBlueprintFn: func(ctx context.Context, blueprintID uint64, targetID int64, appliedBy int64) error {
			applied = true
			return nil
		},
	})

	resp, err := h.ApplyBlueprint(context.Background(), &pb.ApplyBlueprintRequest{
		BlueprintId: 1,
		TargetId:    42,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if !applied {
		t.Error("expected ApplyBlueprint to be called")
	}
}

func TestApplyBlueprint_ServiceError(t *testing.T) {
	h := newBlueprintHandlerForTest(&mockBlueprintSvc{
		applyBlueprintFn: func(ctx context.Context, blueprintID uint64, targetID int64, appliedBy int64) error {
			return service.ErrBlueprintNotFound
		},
	})

	_, err := h.ApplyBlueprint(context.Background(), &pb.ApplyBlueprintRequest{
		BlueprintId: 999,
		TargetId:    42,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}
