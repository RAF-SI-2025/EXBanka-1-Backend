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

type mockLimitSvc struct {
	getEmployeeLimitsFn  func(employeeID int64) (*model.EmployeeLimit, error)
	setEmployeeLimitsFn  func(ctx context.Context, limit model.EmployeeLimit, changedBy int64) (*model.EmployeeLimit, error)
	applyTemplateFn      func(ctx context.Context, employeeID int64, templateName string, changedBy int64) (*model.EmployeeLimit, error)
	listTemplatesFn      func() ([]model.LimitTemplate, error)
	createTemplateFn     func(ctx context.Context, t model.LimitTemplate) (*model.LimitTemplate, error)
}

func (m *mockLimitSvc) GetEmployeeLimits(employeeID int64) (*model.EmployeeLimit, error) {
	if m.getEmployeeLimitsFn != nil {
		return m.getEmployeeLimitsFn(employeeID)
	}
	return &model.EmployeeLimit{EmployeeID: employeeID}, nil
}

func (m *mockLimitSvc) SetEmployeeLimits(ctx context.Context, limit model.EmployeeLimit, changedBy int64) (*model.EmployeeLimit, error) {
	if m.setEmployeeLimitsFn != nil {
		return m.setEmployeeLimitsFn(ctx, limit, changedBy)
	}
	return &limit, nil
}

func (m *mockLimitSvc) ApplyTemplate(ctx context.Context, employeeID int64, templateName string, changedBy int64) (*model.EmployeeLimit, error) {
	if m.applyTemplateFn != nil {
		return m.applyTemplateFn(ctx, employeeID, templateName, changedBy)
	}
	return &model.EmployeeLimit{EmployeeID: employeeID}, nil
}

func (m *mockLimitSvc) ListTemplates() ([]model.LimitTemplate, error) {
	if m.listTemplatesFn != nil {
		return m.listTemplatesFn()
	}
	return nil, nil
}

func (m *mockLimitSvc) CreateTemplate(ctx context.Context, t model.LimitTemplate) (*model.LimitTemplate, error) {
	if m.createTemplateFn != nil {
		return m.createTemplateFn(ctx, t)
	}
	return &t, nil
}

// ---------------------------------------------------------------------------
// GetEmployeeLimits
// ---------------------------------------------------------------------------

func TestGetEmployeeLimits_Success(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		getEmployeeLimitsFn: func(employeeID int64) (*model.EmployeeLimit, error) {
			return &model.EmployeeLimit{
				ID:                    10,
				EmployeeID:            employeeID,
				MaxLoanApprovalAmount: decimal.NewFromInt(50000),
				MaxSingleTransaction:  decimal.NewFromInt(100000),
				MaxDailyTransaction:   decimal.NewFromInt(500000),
				MaxClientDailyLimit:   decimal.NewFromInt(250000),
				MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
			}, nil
		},
	})

	resp, err := h.GetEmployeeLimits(context.Background(), &pb.EmployeeLimitRequest{EmployeeId: 5})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 10 {
		t.Errorf("expected ID=10, got %d", resp.Id)
	}
	if resp.EmployeeId != 5 {
		t.Errorf("expected EmployeeId=5, got %d", resp.EmployeeId)
	}
	if resp.MaxLoanApprovalAmount != "50000" {
		t.Errorf("unexpected MaxLoanApprovalAmount: %s", resp.MaxLoanApprovalAmount)
	}
}

func TestGetEmployeeLimits_ServiceError(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		getEmployeeLimitsFn: func(employeeID int64) (*model.EmployeeLimit, error) {
			return nil, errors.New("not found")
		},
	})

	_, err := h.GetEmployeeLimits(context.Background(), &pb.EmployeeLimitRequest{EmployeeId: 99})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SetEmployeeLimits
// ---------------------------------------------------------------------------

func TestSetEmployeeLimits_Success(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		setEmployeeLimitsFn: func(ctx context.Context, limit model.EmployeeLimit, changedBy int64) (*model.EmployeeLimit, error) {
			limit.ID = 20
			return &limit, nil
		},
	})

	resp, err := h.SetEmployeeLimits(context.Background(), &pb.SetEmployeeLimitsRequest{
		EmployeeId:            7,
		MaxLoanApprovalAmount: "100000",
		MaxSingleTransaction:  "200000",
		MaxDailyTransaction:   "1000000",
		MaxClientDailyLimit:   "500000",
		MaxClientMonthlyLimit: "5000000",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 20 {
		t.Errorf("expected ID=20, got %d", resp.Id)
	}
	if resp.EmployeeId != 7 {
		t.Errorf("expected EmployeeId=7, got %d", resp.EmployeeId)
	}
}

func TestSetEmployeeLimits_InvalidDecimal(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{})

	_, err := h.SetEmployeeLimits(context.Background(), &pb.SetEmployeeLimitsRequest{
		EmployeeId:            7,
		MaxLoanApprovalAmount: "not-a-number",
		MaxSingleTransaction:  "100",
		MaxDailyTransaction:   "100",
		MaxClientDailyLimit:   "100",
		MaxClientMonthlyLimit: "100",
	})
	if err == nil {
		t.Fatal("expected error for invalid decimal, got nil")
	}
	if got := grpcCode(err); got != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", got)
	}
}

func TestSetEmployeeLimits_ServiceError(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		setEmployeeLimitsFn: func(ctx context.Context, limit model.EmployeeLimit, changedBy int64) (*model.EmployeeLimit, error) {
			return nil, errors.New("db down")
		},
	})

	_, err := h.SetEmployeeLimits(context.Background(), &pb.SetEmployeeLimitsRequest{
		EmployeeId:            7,
		MaxLoanApprovalAmount: "100000",
		MaxSingleTransaction:  "200000",
		MaxDailyTransaction:   "1000000",
		MaxClientDailyLimit:   "500000",
		MaxClientMonthlyLimit: "5000000",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ApplyLimitTemplate
// ---------------------------------------------------------------------------

func TestApplyLimitTemplate_Success(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		applyTemplateFn: func(ctx context.Context, employeeID int64, templateName string, changedBy int64) (*model.EmployeeLimit, error) {
			return &model.EmployeeLimit{ID: 5, EmployeeID: employeeID}, nil
		},
	})

	resp, err := h.ApplyLimitTemplate(context.Background(), &pb.ApplyLimitTemplateRequest{
		EmployeeId:   3,
		TemplateName: "BasicTeller",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.EmployeeId != 3 {
		t.Errorf("expected EmployeeId=3, got %d", resp.EmployeeId)
	}
}

func TestApplyLimitTemplate_ServiceError(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		applyTemplateFn: func(ctx context.Context, employeeID int64, templateName string, changedBy int64) (*model.EmployeeLimit, error) {
			return nil, errors.New("template not found: NonExistent")
		},
	})

	_, err := h.ApplyLimitTemplate(context.Background(), &pb.ApplyLimitTemplateRequest{
		EmployeeId:   3,
		TemplateName: "NonExistent",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ListLimitTemplates
// ---------------------------------------------------------------------------

func TestListLimitTemplates_Success(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		listTemplatesFn: func() ([]model.LimitTemplate, error) {
			return []model.LimitTemplate{
				{ID: 1, Name: "BasicTeller"},
				{ID: 2, Name: "SeniorAgent"},
			}, nil
		},
	})

	resp, err := h.ListLimitTemplates(context.Background(), &pb.ListLimitTemplatesRequest{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Templates) != 2 {
		t.Errorf("expected 2 templates, got %d", len(resp.Templates))
	}
	if resp.Templates[0].Name != "BasicTeller" {
		t.Errorf("unexpected first template name: %s", resp.Templates[0].Name)
	}
}

func TestListLimitTemplates_ServiceError(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		listTemplatesFn: func() ([]model.LimitTemplate, error) {
			return nil, errors.New("db down")
		},
	})

	_, err := h.ListLimitTemplates(context.Background(), &pb.ListLimitTemplatesRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// CreateLimitTemplate
// ---------------------------------------------------------------------------

func TestCreateLimitTemplate_Success(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		createTemplateFn: func(ctx context.Context, t model.LimitTemplate) (*model.LimitTemplate, error) {
			t.ID = 99
			return &t, nil
		},
	})

	resp, err := h.CreateLimitTemplate(context.Background(), &pb.CreateLimitTemplateRequest{
		Name:                  "CustomTemplate",
		Description:           "A custom template",
		MaxLoanApprovalAmount: "50000",
		MaxSingleTransaction:  "100000",
		MaxDailyTransaction:   "500000",
		MaxClientDailyLimit:   "250000",
		MaxClientMonthlyLimit: "2500000",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 99 {
		t.Errorf("expected ID=99, got %d", resp.Id)
	}
	if resp.Name != "CustomTemplate" {
		t.Errorf("expected Name=CustomTemplate, got %s", resp.Name)
	}
}

func TestCreateLimitTemplate_InvalidDecimal(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{})

	_, err := h.CreateLimitTemplate(context.Background(), &pb.CreateLimitTemplateRequest{
		Name:                  "Bad",
		MaxLoanApprovalAmount: "bad-decimal",
		MaxSingleTransaction:  "100",
		MaxDailyTransaction:   "100",
		MaxClientDailyLimit:   "100",
		MaxClientMonthlyLimit: "100",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", got)
	}
}

func TestCreateLimitTemplate_ServiceError(t *testing.T) {
	h := newLimitHandlerForTest(&mockLimitSvc{
		createTemplateFn: func(ctx context.Context, t model.LimitTemplate) (*model.LimitTemplate, error) {
			return nil, errors.New("already exists duplicate")
		},
	})

	_, err := h.CreateLimitTemplate(context.Background(), &pb.CreateLimitTemplateRequest{
		Name:                  "Duplicate",
		MaxLoanApprovalAmount: "50000",
		MaxSingleTransaction:  "100000",
		MaxDailyTransaction:   "500000",
		MaxClientDailyLimit:   "250000",
		MaxClientMonthlyLimit: "2500000",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.AlreadyExists {
		t.Errorf("expected codes.AlreadyExists, got %v", got)
	}
}
