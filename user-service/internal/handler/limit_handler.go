package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
	"github.com/shopspring/decimal"
)

// LimitGRPCHandler implements the EmployeeLimitServiceServer interface.
type LimitGRPCHandler struct {
	pb.UnimplementedEmployeeLimitServiceServer
	limitSvc *service.LimitService
}

func NewLimitGRPCHandler(limitSvc *service.LimitService) *LimitGRPCHandler {
	return &LimitGRPCHandler{limitSvc: limitSvc}
}

func (h *LimitGRPCHandler) GetEmployeeLimits(ctx context.Context, req *pb.EmployeeLimitRequest) (*pb.EmployeeLimitResponse, error) {
	limit, err := h.limitSvc.GetEmployeeLimits(req.EmployeeId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to get employee limits: %v", err)
	}
	return toEmployeeLimitResponse(limit), nil
}

func (h *LimitGRPCHandler) SetEmployeeLimits(ctx context.Context, req *pb.SetEmployeeLimitsRequest) (*pb.EmployeeLimitResponse, error) {
	maxLoan, err := decimal.NewFromString(req.MaxLoanApprovalAmount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_loan_approval_amount: %v", err)
	}
	maxSingle, err := decimal.NewFromString(req.MaxSingleTransaction)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_single_transaction: %v", err)
	}
	maxDaily, err := decimal.NewFromString(req.MaxDailyTransaction)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_daily_transaction: %v", err)
	}
	maxClientDaily, err := decimal.NewFromString(req.MaxClientDailyLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_client_daily_limit: %v", err)
	}
	maxClientMonthly, err := decimal.NewFromString(req.MaxClientMonthlyLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_client_monthly_limit: %v", err)
	}

	limit := model.EmployeeLimit{
		EmployeeID:            req.EmployeeId,
		MaxLoanApprovalAmount: maxLoan,
		MaxSingleTransaction:  maxSingle,
		MaxDailyTransaction:   maxDaily,
		MaxClientDailyLimit:   maxClientDaily,
		MaxClientMonthlyLimit: maxClientMonthly,
	}

	result, err := h.limitSvc.SetEmployeeLimits(ctx, limit)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to set employee limits: %v", err)
	}
	return toEmployeeLimitResponse(result), nil
}

func (h *LimitGRPCHandler) ApplyLimitTemplate(ctx context.Context, req *pb.ApplyLimitTemplateRequest) (*pb.EmployeeLimitResponse, error) {
	result, err := h.limitSvc.ApplyTemplate(ctx, req.EmployeeId, req.TemplateName)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to apply limit template: %v", err)
	}
	return toEmployeeLimitResponse(result), nil
}

func (h *LimitGRPCHandler) ListLimitTemplates(ctx context.Context, req *pb.ListLimitTemplatesRequest) (*pb.ListLimitTemplatesResponse, error) {
	templates, err := h.limitSvc.ListTemplates()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list limit templates: %v", err)
	}
	resp := &pb.ListLimitTemplatesResponse{}
	for _, t := range templates {
		t := t
		resp.Templates = append(resp.Templates, toLimitTemplateResponse(&t))
	}
	return resp, nil
}

func (h *LimitGRPCHandler) CreateLimitTemplate(ctx context.Context, req *pb.CreateLimitTemplateRequest) (*pb.LimitTemplateResponse, error) {
	maxLoan, err := decimal.NewFromString(req.MaxLoanApprovalAmount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_loan_approval_amount: %v", err)
	}
	maxSingle, err := decimal.NewFromString(req.MaxSingleTransaction)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_single_transaction: %v", err)
	}
	maxDaily, err := decimal.NewFromString(req.MaxDailyTransaction)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_daily_transaction: %v", err)
	}
	maxClientDaily, err := decimal.NewFromString(req.MaxClientDailyLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_client_daily_limit: %v", err)
	}
	maxClientMonthly, err := decimal.NewFromString(req.MaxClientMonthlyLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid max_client_monthly_limit: %v", err)
	}

	tmpl := model.LimitTemplate{
		Name:                  req.Name,
		Description:           req.Description,
		MaxLoanApprovalAmount: maxLoan,
		MaxSingleTransaction:  maxSingle,
		MaxDailyTransaction:   maxDaily,
		MaxClientDailyLimit:   maxClientDaily,
		MaxClientMonthlyLimit: maxClientMonthly,
	}
	result, err := h.limitSvc.CreateTemplate(ctx, tmpl)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create limit template: %v", err)
	}
	return toLimitTemplateResponse(result), nil
}

func toEmployeeLimitResponse(l *model.EmployeeLimit) *pb.EmployeeLimitResponse {
	return &pb.EmployeeLimitResponse{
		Id:                    l.ID,
		EmployeeId:            l.EmployeeID,
		MaxLoanApprovalAmount: l.MaxLoanApprovalAmount.String(),
		MaxSingleTransaction:  l.MaxSingleTransaction.String(),
		MaxDailyTransaction:   l.MaxDailyTransaction.String(),
		MaxClientDailyLimit:   l.MaxClientDailyLimit.String(),
		MaxClientMonthlyLimit: l.MaxClientMonthlyLimit.String(),
	}
}

func toLimitTemplateResponse(t *model.LimitTemplate) *pb.LimitTemplateResponse {
	return &pb.LimitTemplateResponse{
		Id:                    t.ID,
		Name:                  t.Name,
		Description:           t.Description,
		MaxLoanApprovalAmount: t.MaxLoanApprovalAmount.String(),
		MaxSingleTransaction:  t.MaxSingleTransaction.String(),
		MaxDailyTransaction:   t.MaxDailyTransaction.String(),
		MaxClientDailyLimit:   t.MaxClientDailyLimit.String(),
		MaxClientMonthlyLimit: t.MaxClientMonthlyLimit.String(),
	}
}
