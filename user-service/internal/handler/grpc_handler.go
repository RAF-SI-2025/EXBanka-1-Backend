package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
)

// employeeFacade is the minimal interface of EmployeeService used by UserGRPCHandler.
type employeeFacade interface {
	CreateEmployee(ctx context.Context, emp *model.Employee) error
	GetEmployee(id int64) (*model.Employee, error)
	GetByIDs(ids []int64) ([]model.Employee, error)
	ListEmployees(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error)
	UpdateEmployee(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error)
	SetEmployeeRoles(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error
	SetEmployeeAdditionalPermissions(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error
	ResolvePermissions(emp *model.Employee) []string
}

// roleFacade is the minimal interface of RoleService used by UserGRPCHandler.
type roleFacade interface {
	ListRoles() ([]model.Role, error)
	GetRole(id int64) (*model.Role, error)
	CreateRole(name, description string, permissionCodes []string) (*model.Role, error)
	UpdateRolePermissions(roleID int64, permissionCodes []string) error
	AssignPermissionToRole(roleName string, perm string) error
	RevokePermissionFromRole(roleName string, perm string) error
	ListPermissions() ([]model.Permission, error)
}

type UserGRPCHandler struct {
	pb.UnimplementedUserServiceServer
	empService employeeFacade
	roleSvc    roleFacade
}

func NewUserGRPCHandler(empService *service.EmployeeService, roleSvc *service.RoleService) *UserGRPCHandler {
	return &UserGRPCHandler{empService: empService, roleSvc: roleSvc}
}

// newUserHandlerForTest constructs a UserGRPCHandler with interface-typed dependencies
// for use in unit tests (no concrete service instances needed).
func newUserHandlerForTest(empService employeeFacade, roleSvc roleFacade) *UserGRPCHandler {
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
		JMBG:        req.Jmbg,
	}

	if err := h.empService.CreateEmployee(ctx, emp); err != nil {
		return nil, err
	}

	if req.Role != "" {
		if err := h.empService.SetEmployeeRoles(ctx, emp.ID, []string{req.Role}, 0); err != nil {
			return nil, err
		}
		// Reload employee so the response includes the assigned role/permissions.
		updated, err := h.empService.GetEmployee(emp.ID)
		if err == nil {
			emp = updated
		}
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

func (h *UserGRPCHandler) ListEmployeeFullNames(ctx context.Context, req *pb.ListEmployeeFullNamesRequest) (*pb.ListEmployeeFullNamesResponse, error) {
	if len(req.EmployeeIds) == 0 {
		return &pb.ListEmployeeFullNamesResponse{NamesById: map[int64]string{}}, nil
	}
	rows, err := h.empService.GetByIDs(req.EmployeeIds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list employees: %v", err)
	}
	out := make(map[int64]string, len(rows))
	for _, e := range rows {
		out[e.ID] = strings.TrimSpace(e.FirstName + " " + e.LastName)
	}
	return &pb.ListEmployeeFullNamesResponse{NamesById: out}, nil
}

func (h *UserGRPCHandler) ListEmployees(ctx context.Context, req *pb.ListEmployeesRequest) (*pb.ListEmployeesResponse, error) {
	employees, total, err := h.empService.ListEmployees(
		req.EmailFilter, req.NameFilter, req.PositionFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, err
	}

	resp := &pb.ListEmployeesResponse{TotalCount: int32(total), Employees: make([]*pb.EmployeeResponse, 0, len(employees))}
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
	if req.Jmbg != nil {
		updates["jmbg"] = *req.Jmbg
	}

	changedBy := changelog.ExtractChangedBy(ctx)
	emp, err := h.empService.UpdateEmployee(ctx, req.Id, updates, changedBy)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrEmployeeNotFound
		}
		return nil, err
	}
	return toEmployeeResponse(emp, h.empService), nil
}

// ListRoles returns all roles with their permissions.
func (h *UserGRPCHandler) ListRoles(ctx context.Context, req *pb.ListRolesRequest) (*pb.ListRolesResponse, error) {
	roles, err := h.roleSvc.ListRoles()
	if err != nil {
		return nil, err
	}
	pbRoles := make([]*pb.RoleResponse, 0, len(roles))
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
		return nil, err
	}
	return toRoleResponse(role), nil
}

// UpdateRolePermissions replaces the permissions on a role.
func (h *UserGRPCHandler) UpdateRolePermissions(ctx context.Context, req *pb.UpdateRolePermissionsRequest) (*pb.RoleResponse, error) {
	if err := h.roleSvc.UpdateRolePermissions(req.RoleId, req.PermissionCodes); err != nil {
		return nil, err
	}
	role, err := h.roleSvc.GetRole(req.RoleId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "role not found after update")
	}
	return toRoleResponse(role), nil
}

// AssignPermissionToRole grants a single permission to a role. Validates
// against the codegened catalog: an unknown permission returns InvalidArgument
// and a missing role returns NotFound. Sentinels carry their own GRPCStatus.
func (h *UserGRPCHandler) AssignPermissionToRole(ctx context.Context, req *pb.AssignPermissionToRoleRequest) (*pb.AssignPermissionToRoleResponse, error) {
	if err := h.roleSvc.AssignPermissionToRole(req.RoleName, req.Permission); err != nil {
		return nil, err
	}
	return &pb.AssignPermissionToRoleResponse{}, nil
}

// RevokePermissionFromRole removes a single permission grant from a role.
// Idempotent: revoking a permission that was never granted is a no-op success.
func (h *UserGRPCHandler) RevokePermissionFromRole(ctx context.Context, req *pb.RevokePermissionFromRoleRequest) (*pb.RevokePermissionFromRoleResponse, error) {
	if err := h.roleSvc.RevokePermissionFromRole(req.RoleName, req.Permission); err != nil {
		return nil, err
	}
	return &pb.RevokePermissionFromRoleResponse{}, nil
}

// ListPermissions returns all permissions.
func (h *UserGRPCHandler) ListPermissions(ctx context.Context, req *pb.ListPermissionsRequest) (*pb.ListPermissionsResponse, error) {
	perms, err := h.roleSvc.ListPermissions()
	if err != nil {
		return nil, err
	}
	pbPerms := make([]*pb.PermissionResponse, 0, len(perms))
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
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.empService.SetEmployeeRoles(ctx, req.EmployeeId, req.RoleNames, changedBy); err != nil {
		return nil, err
	}
	emp, err := h.empService.GetEmployee(req.EmployeeId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "employee not found")
	}
	return toEmployeeResponse(emp, h.empService), nil
}

// SetEmployeeAdditionalPermissions replaces the additional permissions for an employee.
func (h *UserGRPCHandler) SetEmployeeAdditionalPermissions(ctx context.Context, req *pb.SetEmployeePermissionsRequest) (*pb.EmployeeResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	if err := h.empService.SetEmployeeAdditionalPermissions(ctx, req.EmployeeId, req.PermissionCodes, changedBy); err != nil {
		return nil, err
	}
	emp, err := h.empService.GetEmployee(req.EmployeeId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "employee not found")
	}
	return toEmployeeResponse(emp, h.empService), nil
}

func toEmployeeResponse(emp *model.Employee, empSvc employeeFacade) *pb.EmployeeResponse {
	permissions := empSvc.ResolvePermissions(emp)
	roleNames := extractRoleNames(emp.Roles)

	// Collect additional permission codes
	additionalPerms := make([]string, 0, len(emp.AdditionalPermissions))
	for _, p := range emp.AdditionalPermissions {
		additionalPerms = append(additionalPerms, p.Code)
	}

	// Populate legacy Role field from first role for backward compat with API consumers
	legacyRole := ""
	if len(roleNames) > 0 {
		legacyRole = roleNames[0]
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
