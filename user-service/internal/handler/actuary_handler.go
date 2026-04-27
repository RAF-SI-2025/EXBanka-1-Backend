package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
	"github.com/shopspring/decimal"
)

// actuaryServiceFacade is the narrow interface of ActuaryService used by ActuaryGRPCHandler.
type actuaryServiceFacade interface {
	ListActuaries(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error)
	GetActuaryInfo(employeeID int64) (*model.ActuaryLimit, *model.Employee, error)
	SetActuaryLimit(ctx context.Context, employeeID int64, limitAmount decimal.Decimal, changedBy int64) (*model.ActuaryLimit, error)
	ResetUsedLimit(ctx context.Context, employeeID int64, changedBy int64) (*model.ActuaryLimit, error)
	SetNeedApproval(ctx context.Context, employeeID int64, needApproval bool, changedBy int64) (*model.ActuaryLimit, error)
	UpdateUsedLimit(ctx context.Context, id int64, amount decimal.Decimal) (*model.ActuaryLimit, error)
}

type ActuaryGRPCHandler struct {
	pb.UnimplementedActuaryServiceServer
	svc actuaryServiceFacade
}

func NewActuaryGRPCHandler(svc *service.ActuaryService) *ActuaryGRPCHandler {
	return &ActuaryGRPCHandler{svc: svc}
}

// newActuaryHandlerForTest constructs an ActuaryGRPCHandler with an interface-typed
// dependency for use in unit tests.
func newActuaryHandlerForTest(svc actuaryServiceFacade) *ActuaryGRPCHandler {
	return &ActuaryGRPCHandler{svc: svc}
}

func (h *ActuaryGRPCHandler) ListActuaries(ctx context.Context, req *pb.ListActuariesRequest) (*pb.ListActuariesResponse, error) {
	rows, total, err := h.svc.ListActuaries(req.Search, req.Position, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, err
	}

	resp := &pb.ListActuariesResponse{TotalCount: total, Actuaries: make([]*pb.ActuaryInfo, 0, len(rows))}
	for _, r := range rows {
		role := "agent"
		// Position-based role determination
		if r.Position == "supervisor" || r.Position == "admin" {
			role = "supervisor"
		}
		resp.Actuaries = append(resp.Actuaries, &pb.ActuaryInfo{
			Id:           r.ActuaryLimitID(),
			EmployeeId:   uint64(r.EmployeeID),
			FirstName:    r.FirstName,
			LastName:     r.LastName,
			Email:        r.Email,
			Role:         role,
			Limit:        decimal.NewFromFloat(r.Limit).String(),
			UsedLimit:    decimal.NewFromFloat(r.UsedLimit).String(),
			NeedApproval: r.NeedApproval,
		})
	}
	return resp, nil
}

func (h *ActuaryGRPCHandler) GetActuaryInfo(ctx context.Context, req *pb.GetActuaryInfoRequest) (*pb.ActuaryInfo, error) {
	limit, emp, err := h.svc.GetActuaryInfo(int64(req.EmployeeId))
	if err != nil {
		return nil, err
	}
	role := "agent"
	for _, r := range emp.Roles {
		if r.Name == "EmployeeSupervisor" || r.Name == "EmployeeAdmin" {
			role = "supervisor"
			break
		}
	}
	return &pb.ActuaryInfo{
		Id:           uint64(limit.ID),
		EmployeeId:   uint64(limit.EmployeeID),
		FirstName:    emp.FirstName,
		LastName:     emp.LastName,
		Email:        emp.Email,
		Role:         role,
		Limit:        limit.Limit.String(),
		UsedLimit:    limit.UsedLimit.String(),
		NeedApproval: limit.NeedApproval,
	}, nil
}

func (h *ActuaryGRPCHandler) SetActuaryLimit(ctx context.Context, req *pb.SetActuaryLimitRequest) (*pb.ActuaryInfo, error) {
	limitVal, err := decimal.NewFromString(req.Limit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid limit value: %v", err)
	}
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.svc.SetActuaryLimit(ctx, int64(req.Id), limitVal, changedBy)
	if err != nil {
		return nil, err
	}
	return toActuaryInfoFromLimit(result), nil
}

func (h *ActuaryGRPCHandler) ResetActuaryUsedLimit(ctx context.Context, req *pb.ResetActuaryUsedLimitRequest) (*pb.ActuaryInfo, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.svc.ResetUsedLimit(ctx, int64(req.Id), changedBy)
	if err != nil {
		return nil, err
	}
	return toActuaryInfoFromLimit(result), nil
}

func (h *ActuaryGRPCHandler) SetNeedApproval(ctx context.Context, req *pb.SetNeedApprovalRequest) (*pb.ActuaryInfo, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	result, err := h.svc.SetNeedApproval(ctx, int64(req.Id), req.NeedApproval, changedBy)
	if err != nil {
		return nil, err
	}
	return toActuaryInfoFromLimit(result), nil
}

func (h *ActuaryGRPCHandler) UpdateUsedLimit(ctx context.Context, req *pb.UpdateUsedLimitRequest) (*pb.ActuaryInfo, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	result, err := h.svc.UpdateUsedLimit(ctx, int64(req.Id), amount)
	if err != nil {
		return nil, err
	}
	return toActuaryInfoFromLimit(result), nil
}

func toActuaryInfoFromLimit(limit *model.ActuaryLimit) *pb.ActuaryInfo {
	return &pb.ActuaryInfo{
		Id:           uint64(limit.ID),
		EmployeeId:   uint64(limit.EmployeeID),
		Limit:        limit.Limit.String(),
		UsedLimit:    limit.UsedLimit.String(),
		NeedApproval: limit.NeedApproval,
	}
}
