package handler

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
)

type UserGRPCHandler struct {
	pb.UnimplementedUserServiceServer
	empService *service.EmployeeService
}

func NewUserGRPCHandler(empService *service.EmployeeService) *UserGRPCHandler {
	return &UserGRPCHandler{empService: empService}
}

func (h *UserGRPCHandler) CreateEmployee(ctx context.Context, req *pb.CreateEmployeeRequest) (*pb.EmployeeResponse, error) {
	dob := time.Unix(req.DateOfBirth, 0)
	emp := &model.Employee{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: dob,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		Role:        req.Role,
		Active:      req.Active,
	}

	if err := h.empService.CreateEmployee(ctx, emp); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create employee: %v", err)
	}
	return toEmployeeResponse(emp), nil
}

func (h *UserGRPCHandler) GetEmployee(ctx context.Context, req *pb.GetEmployeeRequest) (*pb.EmployeeResponse, error) {
	emp, err := h.empService.GetEmployee(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "employee not found")
	}
	return toEmployeeResponse(emp), nil
}

func (h *UserGRPCHandler) ListEmployees(ctx context.Context, req *pb.ListEmployeesRequest) (*pb.ListEmployeesResponse, error) {
	employees, total, err := h.empService.ListEmployees(
		req.EmailFilter, req.NameFilter, req.PositionFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list employees: %v", err)
	}

	resp := &pb.ListEmployeesResponse{TotalCount: int32(total)}
	for _, emp := range employees {
		resp.Employees = append(resp.Employees, toEmployeeResponse(&emp))
	}
	return resp, nil
}

func (h *UserGRPCHandler) UpdateEmployee(ctx context.Context, req *pb.UpdateEmployeeRequest) (*pb.EmployeeResponse, error) {
	updates := make(map[string]interface{})
	if req.LastName != nil {
		updates["last_name"] = *req.LastName
	}
	if req.Gender != nil {
		updates["gender"] = *req.Gender
	}
	if req.Phone != nil {
		updates["phone"] = *req.Phone
	}
	if req.Address != nil {
		updates["address"] = *req.Address
	}
	if req.Position != nil {
		updates["position"] = *req.Position
	}
	if req.Department != nil {
		updates["department"] = *req.Department
	}
	if req.Role != nil {
		updates["role"] = *req.Role
	}
	if req.Active != nil {
		updates["active"] = *req.Active
	}

	emp, err := h.empService.UpdateEmployee(req.Id, updates)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "employee not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to update: %v", err)
	}
	return toEmployeeResponse(emp), nil
}

func (h *UserGRPCHandler) ValidateCredentials(ctx context.Context, req *pb.ValidateCredentialsRequest) (*pb.ValidateCredentialsResponse, error) {
	emp, valid := h.empService.ValidateCredentials(req.Email, req.Password)
	if !valid {
		return &pb.ValidateCredentialsResponse{Valid: false}, nil
	}
	return &pb.ValidateCredentialsResponse{
		Valid:       true,
		UserId:      emp.ID,
		Email:       emp.Email,
		Role:        emp.Role,
		Permissions: service.GetPermissions(emp.Role),
	}, nil
}

func (h *UserGRPCHandler) GetUserByEmail(ctx context.Context, req *pb.GetUserByEmailRequest) (*pb.UserResponse, error) {
	emp, err := h.empService.GetByEmail(req.Email)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "user not found")
	}
	return &pb.UserResponse{
		Id:           emp.ID,
		Email:        emp.Email,
		Role:         emp.Role,
		Permissions:  service.GetPermissions(emp.Role),
		PasswordHash: emp.PasswordHash,
		Active:       emp.Active,
	}, nil
}

func (h *UserGRPCHandler) SetPassword(ctx context.Context, req *pb.SetPasswordRequest) (*pb.SetPasswordResponse, error) {
	if err := h.empService.SetPassword(req.UserId, req.PasswordHash); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set password: %v", err)
	}
	return &pb.SetPasswordResponse{Success: true}, nil
}

func toEmployeeResponse(emp *model.Employee) *pb.EmployeeResponse {
	return &pb.EmployeeResponse{
		Id:          emp.ID,
		FirstName:   emp.FirstName,
		LastName:    emp.LastName,
		DateOfBirth: emp.DateOfBirth.Unix(),
		Gender:      emp.Gender,
		Email:       emp.Email,
		Phone:       emp.Phone,
		Address:     emp.Address,
		Username:    emp.Username,
		Position:    emp.Position,
		Department:  emp.Department,
		Active:      emp.Active,
		Role:        emp.Role,
		Permissions: service.GetPermissions(emp.Role),
	}
}
