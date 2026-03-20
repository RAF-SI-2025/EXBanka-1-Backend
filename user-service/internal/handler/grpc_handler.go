package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"), strings.Contains(msg, "must not"),
		strings.Contains(msg, "must have"), strings.Contains(msg, "must contain"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "insufficient funds"), strings.Contains(msg, "limit exceeded"),
		strings.Contains(msg, "spending limit"):
		return codes.FailedPrecondition
	case strings.Contains(msg, "locked"), strings.Contains(msg, "max attempts"),
		strings.Contains(msg, "failed attempts"):
		return codes.ResourceExhausted
	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"):
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}

type UserGRPCHandler struct {
	pb.UnimplementedUserServiceServer
	empService *service.EmployeeService
	roleSvc    *service.RoleService
}

func NewUserGRPCHandler(empService *service.EmployeeService, roleSvc *service.RoleService) *UserGRPCHandler {
	return &UserGRPCHandler{empService: empService, roleSvc: roleSvc}
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
		JMBG:        req.Jmbg,
	}

	if err := h.empService.CreateEmployee(ctx, emp); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create employee: %v", err)
	}
	return toEmployeeResponse(emp, h.empService), nil
}

func (h *UserGRPCHandler) GetEmployee(ctx context.Context, req *pb.GetEmployeeRequest) (*pb.EmployeeResponse, error) {
	emp, err := h.empService.GetEmployee(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "employee not found")
	}
	return toEmployeeResponse(emp, h.empService), nil
}

func (h *UserGRPCHandler) ListEmployees(ctx context.Context, req *pb.ListEmployeesRequest) (*pb.ListEmployeesResponse, error) {
	employees, total, err := h.empService.ListEmployees(
		req.EmailFilter, req.NameFilter, req.PositionFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list employees: %v", err)
	}

	resp := &pb.ListEmployeesResponse{TotalCount: int32(total)}
	for _, emp := range employees {
		resp.Employees = append(resp.Employees, toEmployeeResponse(&emp, h.empService))
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
	if req.Jmbg != nil {
		updates["jmbg"] = *req.Jmbg
	}

	emp, err := h.empService.UpdateEmployee(ctx, req.Id, updates)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "employee not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to update: %v", err)
	}
	return toEmployeeResponse(emp, h.empService), nil
}

func (h *UserGRPCHandler) ValidateCredentials(ctx context.Context, req *pb.ValidateCredentialsRequest) (*pb.ValidateCredentialsResponse, error) {
	emp, valid := h.empService.ValidateCredentials(req.Email, req.Password)
	if !valid {
		return &pb.ValidateCredentialsResponse{Valid: false}, nil
	}
	permissions := h.empService.ResolvePermissions(emp)
	roleNames := extractRoleNames(emp.Roles)
	// Fall back to legacy Role field if multi-role not yet populated
	if len(roleNames) == 0 && emp.Role != "" {
		roleNames = []string{emp.Role}
	}
	legacyRole := emp.Role
	if len(roleNames) > 0 {
		legacyRole = roleNames[0]
	}
	return &pb.ValidateCredentialsResponse{
		Valid:       true,
		UserId:      emp.ID,
		Email:       emp.Email,
		Role:        legacyRole,
		Permissions: permissions,
		Roles:       roleNames,
	}, nil
}

func (h *UserGRPCHandler) GetUserByEmail(ctx context.Context, req *pb.GetUserByEmailRequest) (*pb.UserResponse, error) {
	emp, err := h.empService.GetByEmail(req.Email)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "user not found")
	}
	permissions := h.empService.ResolvePermissions(emp)
	roleNames := extractRoleNames(emp.Roles)
	if len(roleNames) == 0 && emp.Role != "" {
		roleNames = []string{emp.Role}
	}
	legacyRole := emp.Role
	if len(roleNames) > 0 {
		legacyRole = roleNames[0]
	}
	return &pb.UserResponse{
		Id:           emp.ID,
		Email:        emp.Email,
		Role:         legacyRole,
		Roles:        roleNames,
		Permissions:  permissions,
		PasswordHash: emp.PasswordHash,
		Active:       emp.Active,
	}, nil
}

func (h *UserGRPCHandler) SetPassword(ctx context.Context, req *pb.SetPasswordRequest) (*pb.SetPasswordResponse, error) {
	if err := h.empService.SetPassword(req.UserId, req.PasswordHash); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to set password: %v", err)
	}
	return &pb.SetPasswordResponse{Success: true}, nil
}

// ListRoles returns all roles with their permissions.
func (h *UserGRPCHandler) ListRoles(ctx context.Context, req *pb.ListRolesRequest) (*pb.ListRolesResponse, error) {
	roles, err := h.roleSvc.ListRoles()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list roles: %v", err)
	}
	var pbRoles []*pb.RoleResponse
	for _, r := range roles {
		pbRoles = append(pbRoles, toRoleResponse(&r))
	}
	return &pb.ListRolesResponse{Roles: pbRoles}, nil
}

// GetRole returns a single role by ID.
func (h *UserGRPCHandler) GetRole(ctx context.Context, req *pb.GetRoleRequest) (*pb.RoleResponse, error) {
	role, err := h.roleSvc.GetRole(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "role not found")
	}
	return toRoleResponse(role), nil
}

// CreateRole creates a new role.
func (h *UserGRPCHandler) CreateRole(ctx context.Context, req *pb.CreateRoleRequest) (*pb.RoleResponse, error) {
	role, err := h.roleSvc.CreateRole(req.Name, req.Description, req.PermissionCodes)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create role: %v", err)
	}
	return toRoleResponse(role), nil
}

// UpdateRolePermissions replaces the permissions on a role.
func (h *UserGRPCHandler) UpdateRolePermissions(ctx context.Context, req *pb.UpdateRolePermissionsRequest) (*pb.RoleResponse, error) {
	if err := h.roleSvc.UpdateRolePermissions(req.RoleId, req.PermissionCodes); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to update role permissions: %v", err)
	}
	role, err := h.roleSvc.GetRole(req.RoleId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "role not found after update")
	}
	return toRoleResponse(role), nil
}

// ListPermissions returns all permissions.
func (h *UserGRPCHandler) ListPermissions(ctx context.Context, req *pb.ListPermissionsRequest) (*pb.ListPermissionsResponse, error) {
	perms, err := h.roleSvc.ListPermissions()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list permissions: %v", err)
	}
	var pbPerms []*pb.PermissionResponse
	for _, p := range perms {
		pbPerms = append(pbPerms, &pb.PermissionResponse{
			Id:          p.ID,
			Code:        p.Code,
			Description: p.Description,
			Category:    p.Category,
		})
	}
	return &pb.ListPermissionsResponse{Permissions: pbPerms}, nil
}

// SetEmployeeRoles replaces the roles for an employee.
func (h *UserGRPCHandler) SetEmployeeRoles(ctx context.Context, req *pb.SetEmployeeRolesRequest) (*pb.EmployeeResponse, error) {
	if err := h.empService.SetEmployeeRoles(ctx, req.EmployeeId, req.RoleNames); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to set employee roles: %v", err)
	}
	emp, err := h.empService.GetEmployee(req.EmployeeId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "employee not found")
	}
	return toEmployeeResponse(emp, h.empService), nil
}

// SetEmployeeAdditionalPermissions replaces the additional permissions for an employee.
func (h *UserGRPCHandler) SetEmployeeAdditionalPermissions(ctx context.Context, req *pb.SetEmployeePermissionsRequest) (*pb.EmployeeResponse, error) {
	if err := h.empService.SetEmployeeAdditionalPermissions(ctx, req.EmployeeId, req.PermissionCodes); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to set employee permissions: %v", err)
	}
	emp, err := h.empService.GetEmployee(req.EmployeeId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "employee not found")
	}
	return toEmployeeResponse(emp, h.empService), nil
}

func toEmployeeResponse(emp *model.Employee, empSvc *service.EmployeeService) *pb.EmployeeResponse {
	permissions := empSvc.ResolvePermissions(emp)
	roleNames := extractRoleNames(emp.Roles)
	// Fall back to legacy Role field if multi-role not yet populated
	if len(roleNames) == 0 && emp.Role != "" {
		roleNames = []string{emp.Role}
	}
	legacyRole := emp.Role
	if len(roleNames) > 0 {
		legacyRole = roleNames[0]
	}

	// Collect additional permission codes
	additionalPerms := make([]string, 0, len(emp.AdditionalPermissions))
	for _, p := range emp.AdditionalPermissions {
		additionalPerms = append(additionalPerms, p.Code)
	}

	return &pb.EmployeeResponse{
		Id:                    emp.ID,
		FirstName:             emp.FirstName,
		LastName:              emp.LastName,
		DateOfBirth:           emp.DateOfBirth.Unix(),
		Gender:                emp.Gender,
		Email:                 emp.Email,
		Phone:                 emp.Phone,
		Address:               emp.Address,
		Username:              emp.Username,
		Position:              emp.Position,
		Department:            emp.Department,
		Active:                emp.Active,
		Role:                  legacyRole,
		Permissions:           permissions,
		Jmbg:                  emp.JMBG,
		Roles:                 roleNames,
		AdditionalPermissions: additionalPerms,
	}
}

func toRoleResponse(role *model.Role) *pb.RoleResponse {
	permCodes := make([]string, 0, len(role.Permissions))
	for _, p := range role.Permissions {
		permCodes = append(permCodes, p.Code)
	}
	return &pb.RoleResponse{
		Id:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		Permissions: permCodes,
	}
}

func extractRoleNames(roles []model.Role) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}
