package handler

import (
	"context"
	"errors"
	"testing"

	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockEmpSvc struct {
	createFn       func(ctx context.Context, emp *model.Employee) error
	getFn          func(id int64) (*model.Employee, error)
	listFn         func(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error)
	updateFn       func(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error)
	setRolesFn     func(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error
	setPermsFn     func(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error
	resolvePermsFn func(emp *model.Employee) []string
}

func (m *mockEmpSvc) CreateEmployee(ctx context.Context, emp *model.Employee) error {
	if m.createFn != nil {
		return m.createFn(ctx, emp)
	}
	return nil
}

func (m *mockEmpSvc) GetEmployee(id int64) (*model.Employee, error) {
	if m.getFn != nil {
		return m.getFn(id)
	}
	return nil, nil
}

func (m *mockEmpSvc) GetByIDs(ids []int64) ([]model.Employee, error) {
	return nil, nil
}

func (m *mockEmpSvc) ListEmployees(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error) {
	if m.listFn != nil {
		return m.listFn(emailFilter, nameFilter, positionFilter, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockEmpSvc) UpdateEmployee(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error) {
	if m.updateFn != nil {
		return m.updateFn(ctx, id, updates, changedBy)
	}
	return nil, nil
}

func (m *mockEmpSvc) SetEmployeeRoles(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
	if m.setRolesFn != nil {
		return m.setRolesFn(ctx, employeeID, roleNames, changedBy)
	}
	return nil
}

func (m *mockEmpSvc) SetEmployeeAdditionalPermissions(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error {
	if m.setPermsFn != nil {
		return m.setPermsFn(ctx, employeeID, permCodes, changedBy)
	}
	return nil
}

func (m *mockEmpSvc) ResolvePermissions(emp *model.Employee) []string {
	if m.resolvePermsFn != nil {
		return m.resolvePermsFn(emp)
	}
	return nil
}

// ---------------------------------------------------------------------------

type mockRoleSvc struct {
	listRolesFn          func() ([]model.Role, error)
	getRoleFn            func(id int64) (*model.Role, error)
	createRoleFn         func(name, description string, perms []string) (*model.Role, error)
	updateRoleFn         func(roleID int64, perms []string) error
	assignPermFn         func(roleName, perm string) error
	revokePermFn         func(roleName, perm string) error
	listPermsFn          func() ([]model.Permission, error)
}

func (m *mockRoleSvc) ListRoles() ([]model.Role, error) {
	if m.listRolesFn != nil {
		return m.listRolesFn()
	}
	return nil, nil
}

func (m *mockRoleSvc) GetRole(id int64) (*model.Role, error) {
	if m.getRoleFn != nil {
		return m.getRoleFn(id)
	}
	return nil, nil
}

func (m *mockRoleSvc) CreateRole(name, description string, permissionCodes []string) (*model.Role, error) {
	if m.createRoleFn != nil {
		return m.createRoleFn(name, description, permissionCodes)
	}
	return nil, nil
}

func (m *mockRoleSvc) UpdateRolePermissions(roleID int64, permissionCodes []string) error {
	if m.updateRoleFn != nil {
		return m.updateRoleFn(roleID, permissionCodes)
	}
	return nil
}

func (m *mockRoleSvc) AssignPermissionToRole(roleName, perm string) error {
	if m.assignPermFn != nil {
		return m.assignPermFn(roleName, perm)
	}
	return nil
}

func (m *mockRoleSvc) RevokePermissionFromRole(roleName, perm string) error {
	if m.revokePermFn != nil {
		return m.revokePermFn(roleName, perm)
	}
	return nil
}

func (m *mockRoleSvc) ListPermissions() ([]model.Permission, error) {
	if m.listPermsFn != nil {
		return m.listPermsFn()
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	s, ok := status.FromError(err)
	if !ok {
		return codes.Unknown
	}
	return s.Code()
}

// ---------------------------------------------------------------------------
// Employee tests
// ---------------------------------------------------------------------------

func TestCreateEmployee_Success(t *testing.T) {
	emp := &mockEmpSvc{
		createFn: func(ctx context.Context, e *model.Employee) error {
			e.ID = 1
			e.FirstName = "Ana"
			e.LastName = "Jovic"
			e.Email = "ana@bank.rs"
			return nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return nil },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.CreateEmployee(context.Background(), &pb.CreateEmployeeRequest{
		FirstName:   "Ana",
		LastName:    "Jovic",
		Email:       "ana@bank.rs",
		DateOfBirth: 946684800,
		Gender:      "F",
		Username:    "ajovic",
		Jmbg:        "1234567890123",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 1 {
		t.Errorf("expected ID=1, got %d", resp.Id)
	}
}

func TestCreateEmployee_ServiceError(t *testing.T) {
	emp := &mockEmpSvc{
		createFn: func(ctx context.Context, e *model.Employee) error {
			return errors.New("db down")
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.CreateEmployee(context.Background(), &pb.CreateEmployeeRequest{
		FirstName: "Ana", LastName: "Jovic", Email: "ana@bank.rs",
		DateOfBirth: 946684800, Gender: "F", Username: "ajovic", Jmbg: "1234567890123",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Raw (non-sentinel) errors now pass through as Unknown — substring
	// matching has been removed; the logging interceptor preserves the wrap
	// chain for ops visibility while the wire status indicates "untyped".
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

func TestGetEmployee_Success(t *testing.T) {
	emp := &mockEmpSvc{
		getFn: func(id int64) (*model.Employee, error) {
			return &model.Employee{ID: id, FirstName: "Ana"}, nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return nil },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.GetEmployee(context.Background(), &pb.GetEmployeeRequest{Id: 42})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 42 {
		t.Errorf("expected ID=42, got %d", resp.Id)
	}
}

func TestGetEmployee_NotFound(t *testing.T) {
	emp := &mockEmpSvc{
		getFn: func(id int64) (*model.Employee, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.GetEmployee(context.Background(), &pb.GetEmployeeRequest{Id: 99})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Handler still maps GORM record-not-found to NotFound at this site (it
	// uses an explicit status, not mapServiceError).
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

func TestListEmployees_Success(t *testing.T) {
	emp := &mockEmpSvc{
		listFn: func(e, n, p string, page, size int) ([]model.Employee, int64, error) {
			return []model.Employee{
				{ID: 1, FirstName: "Ana"},
				{ID: 2, FirstName: "Marko"},
			}, 2, nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return nil },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.ListEmployees(context.Background(), &pb.ListEmployeesRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.TotalCount != 2 {
		t.Errorf("expected TotalCount=2, got %d", resp.TotalCount)
	}
	if len(resp.Employees) != 2 {
		t.Errorf("expected 2 employees, got %d", len(resp.Employees))
	}
}

func TestListEmployees_ServiceError(t *testing.T) {
	emp := &mockEmpSvc{
		listFn: func(e, n, p string, page, size int) ([]model.Employee, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.ListEmployees(context.Background(), &pb.ListEmployeesRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Untyped errors now pass through as Unknown — see TestSentinel_Passthrough
	// for the new typed-error contract.
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Role tests
// ---------------------------------------------------------------------------

func TestListRoles_Success(t *testing.T) {
	roleSvc := &mockRoleSvc{
		listRolesFn: func() ([]model.Role, error) {
			return []model.Role{
				{ID: 1, Name: "EmployeeBasic", Permissions: []model.Permission{}},
				{ID: 2, Name: "EmployeeAdmin", Permissions: []model.Permission{}},
			}, nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.ListRoles(context.Background(), &pb.ListRolesRequest{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(resp.Roles))
	}
}

func TestListRoles_ServiceError(t *testing.T) {
	roleSvc := &mockRoleSvc{
		listRolesFn: func() ([]model.Role, error) {
			return nil, errors.New("db down")
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.ListRoles(context.Background(), &pb.ListRolesRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

func TestGetRole_Success(t *testing.T) {
	roleSvc := &mockRoleSvc{
		getRoleFn: func(id int64) (*model.Role, error) {
			return &model.Role{ID: id, Name: "EmployeeBasic", Permissions: []model.Permission{}}, nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.GetRole(context.Background(), &pb.GetRoleRequest{Id: 1})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 1 {
		t.Errorf("expected ID=1, got %d", resp.Id)
	}
	if resp.Name != "EmployeeBasic" {
		t.Errorf("expected Name=EmployeeBasic, got %s", resp.Name)
	}
}

func TestGetRole_NotFound(t *testing.T) {
	roleSvc := &mockRoleSvc{
		getRoleFn: func(id int64) (*model.Role, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.GetRole(context.Background(), &pb.GetRoleRequest{Id: 999})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SetEmployeeRoles tests
// ---------------------------------------------------------------------------

func TestSetEmployeeRoles_Success(t *testing.T) {
	emp := &mockEmpSvc{
		setRolesFn: func(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
			return nil
		},
		getFn: func(id int64) (*model.Employee, error) {
			return &model.Employee{ID: id, Roles: []model.Role{{ID: 1, Name: "EmployeeBasic"}}}, nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return []string{"clients.read"} },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.SetEmployeeRoles(context.Background(), &pb.SetEmployeeRolesRequest{
		EmployeeId: 5,
		RoleNames:  []string{"EmployeeBasic"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 5 {
		t.Errorf("expected ID=5, got %d", resp.Id)
	}
}

func TestSetEmployeeRoles_ServiceError(t *testing.T) {
	emp := &mockEmpSvc{
		setRolesFn: func(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
			return errors.New("db down")
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.SetEmployeeRoles(context.Background(), &pb.SetEmployeeRolesRequest{
		EmployeeId: 5,
		RoleNames:  []string{"EmployeeBasic"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// SetEmployeeAdditionalPermissions tests
// ---------------------------------------------------------------------------

func TestSetEmployeeAdditionalPermissions_Success(t *testing.T) {
	emp := &mockEmpSvc{
		setPermsFn: func(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error {
			return nil
		},
		getFn: func(id int64) (*model.Employee, error) {
			return &model.Employee{
				ID: id,
				AdditionalPermissions: []model.Permission{
					{ID: 1, Code: "clients.read"},
				},
			}, nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return []string{"clients.read"} },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.SetEmployeeAdditionalPermissions(context.Background(), &pb.SetEmployeePermissionsRequest{
		EmployeeId:      7,
		PermissionCodes: []string{"clients.read"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 7 {
		t.Errorf("expected ID=7, got %d", resp.Id)
	}
}

// ---------------------------------------------------------------------------
// UpdateRolePermissions tests
// ---------------------------------------------------------------------------

func TestUpdateRolePermissions_Success(t *testing.T) {
	roleSvc := &mockRoleSvc{
		updateRoleFn: func(roleID int64, perms []string) error {
			return nil
		},
		getRoleFn: func(id int64) (*model.Role, error) {
			return &model.Role{
				ID:          id,
				Name:        "EmployeeBasic",
				Permissions: []model.Permission{{ID: 1, Code: "clients.read"}},
			}, nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.UpdateRolePermissions(context.Background(), &pb.UpdateRolePermissionsRequest{
		RoleId:          1,
		PermissionCodes: []string{"clients.read"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 1 {
		t.Errorf("expected role ID=1, got %d", resp.Id)
	}
}

// ---------------------------------------------------------------------------
// ListPermissions tests
// ---------------------------------------------------------------------------

func TestListPermissions_Success(t *testing.T) {
	roleSvc := &mockRoleSvc{
		listPermsFn: func() ([]model.Permission, error) {
			return []model.Permission{
				{ID: 1, Code: "clients.read", Description: "Read clients"},
				{ID: 2, Code: "clients.write", Description: "Write clients"},
			}, nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.ListPermissions(context.Background(), &pb.ListPermissionsRequest{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Permissions) != 2 {
		t.Errorf("expected 2 permissions, got %d", len(resp.Permissions))
	}
	if resp.Permissions[0].Code != "clients.read" {
		t.Errorf("unexpected first permission code: %s", resp.Permissions[0].Code)
	}
}

func TestListPermissions_ServiceError(t *testing.T) {
	roleSvc := &mockRoleSvc{
		listPermsFn: func() ([]model.Permission, error) {
			return nil, errors.New("db down")
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.ListPermissions(context.Background(), &pb.ListPermissionsRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// UpdateEmployee tests
// ---------------------------------------------------------------------------

func TestUpdateEmployee_Success(t *testing.T) {
	lastName := "Petrovic"
	emp := &mockEmpSvc{
		updateFn: func(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error) {
			return &model.Employee{ID: id, FirstName: "Ana", LastName: lastName}, nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return nil },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.UpdateEmployee(context.Background(), &pb.UpdateEmployeeRequest{
		Id:       10,
		LastName: &lastName,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 10 {
		t.Errorf("expected ID=10, got %d", resp.Id)
	}
	if resp.LastName != "Petrovic" {
		t.Errorf("expected LastName=Petrovic, got %s", resp.LastName)
	}
}

func TestUpdateEmployee_NotFound(t *testing.T) {
	emp := &mockEmpSvc{
		updateFn: func(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.UpdateEmployee(context.Background(), &pb.UpdateEmployeeRequest{Id: 99})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", got)
	}
}

func TestUpdateEmployee_ServiceError(t *testing.T) {
	// Inject the typed sentinel — the handler now relies on GRPCStatus, not
	// substring matching, to convey the error class.
	emp := &mockEmpSvc{
		updateFn: func(ctx context.Context, id int64, updates map[string]interface{}, changedBy int64) (*model.Employee, error) {
			return nil, service.ErrEmployeeAlreadyExists
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.UpdateEmployee(context.Background(), &pb.UpdateEmployeeRequest{Id: 5})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.AlreadyExists {
		t.Errorf("expected codes.AlreadyExists, got %v", got)
	}
	if !errors.Is(err, service.ErrEmployeeAlreadyExists) {
		t.Errorf("expected errors.Is to match ErrEmployeeAlreadyExists")
	}
}

// ---------------------------------------------------------------------------
// CreateRole tests
// ---------------------------------------------------------------------------

func TestCreateRole_Success(t *testing.T) {
	roleSvc := &mockRoleSvc{
		createRoleFn: func(name, description string, perms []string) (*model.Role, error) {
			return &model.Role{ID: 10, Name: name, Description: description, Permissions: []model.Permission{}}, nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.CreateRole(context.Background(), &pb.CreateRoleRequest{
		Name:            "CustomRole",
		Description:     "A custom role",
		PermissionCodes: []string{"clients.read"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 10 {
		t.Errorf("expected ID=10, got %d", resp.Id)
	}
	if resp.Name != "CustomRole" {
		t.Errorf("expected Name=CustomRole, got %s", resp.Name)
	}
}

func TestCreateRole_ServiceError(t *testing.T) {
	roleSvc := &mockRoleSvc{
		createRoleFn: func(name, description string, perms []string) (*model.Role, error) {
			return nil, service.ErrEmployeeAlreadyExists
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.CreateRole(context.Background(), &pb.CreateRoleRequest{Name: "Duplicate"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.AlreadyExists {
		t.Errorf("expected codes.AlreadyExists, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// UpdateRolePermissions error path
// ---------------------------------------------------------------------------

func TestUpdateRolePermissions_UpdateError(t *testing.T) {
	roleSvc := &mockRoleSvc{
		updateRoleFn: func(roleID int64, perms []string) error {
			return errors.New("db down")
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.UpdateRolePermissions(context.Background(), &pb.UpdateRolePermissionsRequest{
		RoleId:          1,
		PermissionCodes: []string{"clients.read"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Sentinel passthrough — verify typed sentinels reach the wire intact
// ---------------------------------------------------------------------------

func TestSentinel_Passthrough(t *testing.T) {
	tests := []struct {
		name     string
		sentinel error
		expected codes.Code
	}{
		{"ErrEmployeeNotFound", service.ErrEmployeeNotFound, codes.NotFound},
		{"ErrInvalidJMBG", service.ErrInvalidJMBG, codes.InvalidArgument},
		{"ErrInvalidPassword", service.ErrInvalidPassword, codes.InvalidArgument},
		{"ErrEmployeeAlreadyExists", service.ErrEmployeeAlreadyExists, codes.AlreadyExists},
		{"ErrPermissionNotInCatalog", service.ErrPermissionNotInCatalog, codes.InvalidArgument},
		{"ErrRoleNotFound", service.ErrRoleNotFound, codes.NotFound},
		{"ErrTemplateNotFound", service.ErrTemplateNotFound, codes.NotFound},
		{"ErrBlueprintNotFound", service.ErrBlueprintNotFound, codes.NotFound},
		{"ErrLimitExceedsTemplate", service.ErrLimitExceedsTemplate, codes.FailedPrecondition},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := status.FromError(tc.sentinel)
			if !ok {
				t.Fatalf("sentinel %s does not satisfy GRPCStatus interface", tc.name)
			}
			if s.Code() != tc.expected {
				t.Errorf("expected code=%v, got %v", tc.expected, s.Code())
			}
			// Wrapped sentinels must also resolve via errors.Is
			wrapped := errors.Join(errors.New("context: "), tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is failed for wrapped %s", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SetEmployeeAdditionalPermissions error path
// ---------------------------------------------------------------------------

func TestSetEmployeeAdditionalPermissions_ServiceError(t *testing.T) {
	emp := &mockEmpSvc{
		setPermsFn: func(ctx context.Context, employeeID int64, permCodes []string, changedBy int64) error {
			return errors.New("db down")
		},
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	_, err := h.SetEmployeeAdditionalPermissions(context.Background(), &pb.SetEmployeePermissionsRequest{
		EmployeeId:      7,
		PermissionCodes: []string{"clients.read"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.Unknown {
		t.Errorf("expected codes.Unknown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// CreateEmployee with role assignment
// ---------------------------------------------------------------------------

func TestCreateEmployee_WithRole_Success(t *testing.T) {
	callCount := 0
	emp := &mockEmpSvc{
		createFn: func(ctx context.Context, e *model.Employee) error {
			e.ID = 3
			return nil
		},
		setRolesFn: func(ctx context.Context, employeeID int64, roleNames []string, changedBy int64) error {
			return nil
		},
		getFn: func(id int64) (*model.Employee, error) {
			callCount++
			return &model.Employee{
				ID:        id,
				FirstName: "Ana",
				Roles:     []model.Role{{ID: 1, Name: "EmployeeBasic"}},
			}, nil
		},
		resolvePermsFn: func(e *model.Employee) []string { return []string{"clients.read"} },
	}
	h := newUserHandlerForTest(emp, &mockRoleSvc{})

	resp, err := h.CreateEmployee(context.Background(), &pb.CreateEmployeeRequest{
		FirstName:   "Ana",
		LastName:    "Jovic",
		Email:       "ana@bank.rs",
		DateOfBirth: 946684800,
		Gender:      "F",
		Username:    "ajovic",
		Jmbg:        "1234567890123",
		Role:        "EmployeeBasic",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Id != 3 {
		t.Errorf("expected ID=3, got %d", resp.Id)
	}
	if len(resp.Roles) == 0 {
		t.Error("expected at least one role in response")
	}
}

// ---------------------------------------------------------------------------
// AssignPermissionToRole / RevokePermissionFromRole
// ---------------------------------------------------------------------------

func TestAssignPermissionToRole_Success(t *testing.T) {
	calls := 0
	roleSvc := &mockRoleSvc{
		assignPermFn: func(roleName, perm string) error {
			calls++
			if roleName != "EmployeeBasic" || perm != "clients.read.all" {
				t.Errorf("unexpected args: role=%q perm=%q", roleName, perm)
			}
			return nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.AssignPermissionToRole(context.Background(), &pb.AssignPermissionToRoleRequest{
		RoleName:   "EmployeeBasic",
		Permission: "clients.read.all",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if calls != 1 {
		t.Errorf("expected service called once, got %d", calls)
	}
}

func TestAssignPermissionToRole_PermissionNotInCatalog(t *testing.T) {
	roleSvc := &mockRoleSvc{
		assignPermFn: func(roleName, perm string) error {
			return service.ErrPermissionNotInCatalog
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.AssignPermissionToRole(context.Background(), &pb.AssignPermissionToRoleRequest{
		RoleName:   "EmployeeBasic",
		Permission: "totally.fake.permission",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", got)
	}
}

func TestAssignPermissionToRole_RoleNotFound(t *testing.T) {
	roleSvc := &mockRoleSvc{
		assignPermFn: func(roleName, perm string) error {
			return service.ErrRoleNotFound
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.AssignPermissionToRole(context.Background(), &pb.AssignPermissionToRoleRequest{
		RoleName:   "NoSuchRole",
		Permission: "clients.read.all",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected NotFound, got %v", got)
	}
}

func TestRevokePermissionFromRole_Success(t *testing.T) {
	calls := 0
	roleSvc := &mockRoleSvc{
		revokePermFn: func(roleName, perm string) error {
			calls++
			if roleName != "EmployeeBasic" || perm != "clients.read.all" {
				t.Errorf("unexpected args: role=%q perm=%q", roleName, perm)
			}
			return nil
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	resp, err := h.RevokePermissionFromRole(context.Background(), &pb.RevokePermissionFromRoleRequest{
		RoleName:   "EmployeeBasic",
		Permission: "clients.read.all",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if calls != 1 {
		t.Errorf("expected service called once, got %d", calls)
	}
}

func TestRevokePermissionFromRole_RoleNotFound(t *testing.T) {
	roleSvc := &mockRoleSvc{
		revokePermFn: func(roleName, perm string) error {
			return service.ErrRoleNotFound
		},
	}
	h := newUserHandlerForTest(&mockEmpSvc{}, roleSvc)

	_, err := h.RevokePermissionFromRole(context.Background(), &pb.RevokePermissionFromRoleRequest{
		RoleName:   "NoSuchRole",
		Permission: "clients.read.all",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := grpcCode(err); got != codes.NotFound {
		t.Errorf("expected NotFound, got %v", got)
	}
}
