// user-service/internal/service/role_service_test.go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/permissions"
	"github.com/exbanka/user-service/internal/model"
	"github.com/stretchr/testify/assert"
)

// mockRoleRepo implements RoleRepo for testing.
type mockRoleRepo struct {
	roles           map[string]*model.Role
	byID            map[int64]*model.Role
	nextID          int64
	employeesByRole map[int64][]int64
}

func newMockRoleRepo() *mockRoleRepo {
	return &mockRoleRepo{
		roles:           make(map[string]*model.Role),
		byID:            make(map[int64]*model.Role),
		nextID:          1,
		employeesByRole: make(map[int64][]int64),
	}
}

func (m *mockRoleRepo) Create(role *model.Role) error {
	role.ID = m.nextID
	m.nextID++
	m.roles[role.Name] = role
	m.byID[role.ID] = role
	return nil
}

func (m *mockRoleRepo) GetByID(id int64) (*model.Role, error) {
	r, ok := m.byID[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (m *mockRoleRepo) GetByName(name string) (*model.Role, error) {
	r, ok := m.roles[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (m *mockRoleRepo) GetByNames(names []string) ([]model.Role, error) {
	var result []model.Role
	for _, name := range names {
		if r, ok := m.roles[name]; ok {
			result = append(result, *r)
		}
	}
	return result, nil
}

func (m *mockRoleRepo) List() ([]model.Role, error) {
	var result []model.Role
	for _, r := range m.roles {
		result = append(result, *r)
	}
	return result, nil
}

func (m *mockRoleRepo) Update(role *model.Role) error {
	m.roles[role.Name] = role
	m.byID[role.ID] = role
	return nil
}

func (m *mockRoleRepo) SetPermissions(roleID int64, permissions []model.Permission) error {
	r, ok := m.byID[roleID]
	if !ok {
		return errors.New("not found")
	}
	r.Permissions = permissions
	return nil
}

func (m *mockRoleRepo) Delete(id int64) error {
	r, ok := m.byID[id]
	if !ok {
		return errors.New("not found")
	}
	delete(m.roles, r.Name)
	delete(m.byID, id)
	return nil
}

func (m *mockRoleRepo) ListEmployeeIDsByRole(roleID int64) ([]int64, error) {
	ids, ok := m.employeesByRole[roleID]
	if !ok {
		return []int64{}, nil
	}
	return ids, nil
}

// fakeRolePermPublisher records calls to PublishRolePermissionsChanged.
type fakeRolePermPublisher struct {
	calls []kafkamsg.RolePermissionsChangedMessage
	err   error
}

func (f *fakeRolePermPublisher) PublishRolePermissionsChanged(_ context.Context, msg kafkamsg.RolePermissionsChangedMessage) error {
	f.calls = append(f.calls, msg)
	return f.err
}

// mockPermRepo implements PermissionRepo for testing.
type mockPermRepo struct {
	perms  map[string]*model.Permission
	byID   map[int64]*model.Permission
	nextID int64
}

func newMockPermRepo() *mockPermRepo {
	return &mockPermRepo{
		perms:  make(map[string]*model.Permission),
		byID:   make(map[int64]*model.Permission),
		nextID: 1,
	}
}

func (m *mockPermRepo) Create(p *model.Permission) error {
	p.ID = m.nextID
	m.nextID++
	m.perms[p.Code] = p
	m.byID[p.ID] = p
	return nil
}

func (m *mockPermRepo) GetByCode(code string) (*model.Permission, error) {
	p, ok := m.perms[code]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}

func (m *mockPermRepo) GetByID(id int64) (*model.Permission, error) {
	p, ok := m.byID[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}

func (m *mockPermRepo) List() ([]model.Permission, error) {
	var result []model.Permission
	for _, p := range m.perms {
		result = append(result, *p)
	}
	return result, nil
}

func (m *mockPermRepo) ListByCategory(category string) ([]model.Permission, error) {
	var result []model.Permission
	for _, p := range m.perms {
		if p.Category == category {
			result = append(result, *p)
		}
	}
	return result, nil
}

func (m *mockPermRepo) ListByCodes(codes []string) ([]model.Permission, error) {
	var result []model.Permission
	for _, code := range codes {
		if p, ok := m.perms[code]; ok {
			result = append(result, *p)
		}
	}
	return result, nil
}

func (m *mockPermRepo) Delete(id int64) error {
	p, ok := m.byID[id]
	if !ok {
		return errors.New("not found")
	}
	delete(m.perms, p.Code)
	delete(m.byID, id)
	return nil
}

func TestRoleService_ValidRole(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	// Seed a role
	_ = roleRepo.Create(&model.Role{Name: "EmployeeBasic"})

	assert.True(t, svc.ValidRole("EmployeeBasic"))
	assert.False(t, svc.ValidRole("InvalidRole"))
	assert.False(t, svc.ValidRole(""))
}

func TestRoleService_GetPermissionsForRoles(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	codes, err := svc.GetPermissionsForRoles([]string{"EmployeeBasic"})
	assert.NoError(t, err)
	assert.Contains(t, codes, string(permissions.Clients.Read.Assigned))
	assert.Contains(t, codes, string(permissions.Accounts.Read.Own))

	adminCodes, err := svc.GetPermissionsForRoles([]string{"EmployeeAdmin"})
	assert.NoError(t, err)
	assert.Contains(t, adminCodes, string(permissions.Employees.Permissions.Assign))
	assert.Contains(t, adminCodes, string(permissions.Limits.Employee.Update))
}

// seedDefaultRolesIntoMocks populates the mock repos with roles and
// permissions from the codegened catalog so legacy unit tests that used to
// call SeedRolesAndPermissions on mocks still have realistic state.
func seedDefaultRolesIntoMocks(t *testing.T, roleRepo *mockRoleRepo, permRepo *mockPermRepo) {
	t.Helper()
	for _, p := range permissions.Catalog {
		if _, err := permRepo.GetByCode(string(p)); err != nil {
			_ = permRepo.Create(&model.Permission{Code: string(p)})
		}
	}
	for roleName, perms := range permissions.DefaultRoles {
		var permModels []model.Permission
		for _, p := range perms {
			pm, err := permRepo.GetByCode(string(p))
			if err == nil {
				permModels = append(permModels, *pm)
			}
		}
		_ = roleRepo.Create(&model.Role{Name: roleName, Permissions: permModels})
	}
}

func TestRoleService_CreateRole(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	// Add some permissions first
	_ = permRepo.Create(&model.Permission{Code: "clients.read.all", Category: "clients"})
	_ = permRepo.Create(&model.Permission{Code: "accounts.read.all", Category: "accounts"})

	role, err := svc.CreateRole("TestRole", "A test role", []string{"clients.read.all", "accounts.read.all"})
	assert.NoError(t, err)
	assert.Equal(t, "TestRole", role.Name)
	assert.Len(t, role.Permissions, 2)
}

func TestRoleService_UpdateRolePermissions(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	_ = permRepo.Create(&model.Permission{Code: "clients.read.all"})
	_ = permRepo.Create(&model.Permission{Code: "accounts.read.all"})
	_ = roleRepo.Create(&model.Role{ID: 1, Name: "TestRole"})

	err := svc.UpdateRolePermissions(1, []string{"clients.read.all", "accounts.read.all"})
	assert.NoError(t, err)

	role, _ := roleRepo.GetByID(1)
	assert.Len(t, role.Permissions, 2)
}

func TestRoleService_ListPermissions(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	perms, err := svc.ListPermissions()
	assert.NoError(t, err)
	assert.NotEmpty(t, perms)
	// Should match the codegened catalog size.
	assert.Equal(t, len(permissions.Catalog), len(perms))
}

func TestRoleService_GetRolesByNames(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	roles, err := svc.GetRolesByNames([]string{"EmployeeBasic", "EmployeeAdmin"})
	assert.NoError(t, err)
	assert.Len(t, roles, 2)

	names := make(map[string]bool)
	for _, r := range roles {
		names[r.Name] = true
	}
	assert.True(t, names["EmployeeBasic"])
	assert.True(t, names["EmployeeAdmin"])
}

func TestRoleService_GetPermissionsByCodes(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	codes := []string{string(permissions.Clients.Read.All), string(permissions.Accounts.Read.All)}
	perms, err := svc.GetPermissionsByCodes(codes)
	assert.NoError(t, err)
	assert.Len(t, perms, 2)

	got := make(map[string]bool)
	for _, p := range perms {
		got[p.Code] = true
	}
	assert.True(t, got[string(permissions.Clients.Read.All)])
	assert.True(t, got[string(permissions.Accounts.Read.All)])
}

func TestSeedRoles_EmployeeAdminHasSecuritiesManage(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	seedDefaultRolesIntoMocks(t, roleRepo, permRepo)

	adminCodes, err := svc.GetPermissionsForRoles([]string{"EmployeeAdmin"})
	assert.NoError(t, err)
	assert.Contains(t, adminCodes, string(permissions.Securities.Manage.Catalog),
		"EmployeeAdmin should have the securities.manage.catalog permission")

	// Also verify Basic + Agent do NOT have this permission. (Supervisor
	// inherits it from the catalog so it's expected to have it too.)
	for _, roleName := range []string{"EmployeeBasic", "EmployeeAgent"} {
		codes, err2 := svc.GetPermissionsForRoles([]string{roleName})
		assert.NoError(t, err2)
		assert.NotContains(t, codes, string(permissions.Securities.Manage.Catalog),
			"%s should NOT have the securities.manage.catalog permission", roleName)
	}
}

func TestRoleService_UpdateRolePermissions_PublishesEvent(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()

	role := &model.Role{Name: "EmployeeAgent"}
	if err := roleRepo.Create(role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := permRepo.Create(&model.Permission{Code: "clients.read.all"}); err != nil {
		t.Fatalf("create perm: %v", err)
	}

	roleRepo.employeesByRole[role.ID] = []int64{42, 43}

	pub := &fakeRolePermPublisher{}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	before := time.Now().Unix()
	if err := svc.UpdateRolePermissions(role.ID, []string{"clients.read.all"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	after := time.Now().Unix()

	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(pub.calls))
	}
	got := pub.calls[0]
	if got.RoleID != role.ID || got.RoleName != "EmployeeAgent" {
		t.Errorf("role identity: got %+v", got)
	}
	if got.Source != "update_role_permissions" {
		t.Errorf("source: got %q", got.Source)
	}
	if len(got.AffectedEmployeeIDs) != 2 || got.AffectedEmployeeIDs[0] != 42 || got.AffectedEmployeeIDs[1] != 43 {
		t.Errorf("affected ids: got %v", got.AffectedEmployeeIDs)
	}
	if got.ChangedAt < before || got.ChangedAt > after {
		t.Errorf("ChangedAt %d not within [%d,%d]", got.ChangedAt, before, after)
	}
}

func TestRoleService_UpdateRolePermissions_KafkaFailureDoesNotFailUpdate(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "EmployeeAgent"}
	if err := roleRepo.Create(role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := permRepo.Create(&model.Permission{Code: "clients.read.all"}); err != nil {
		t.Fatalf("create perm: %v", err)
	}
	roleRepo.employeesByRole[role.ID] = []int64{1}

	pub := &fakeRolePermPublisher{err: errors.New("kafka down")}
	svc := NewRoleService(roleRepo, permRepo).WithPublisher(pub)

	if err := svc.UpdateRolePermissions(role.ID, []string{"clients.read.all"}); err != nil {
		t.Fatalf("update should not propagate kafka errors: %v", err)
	}
}

func TestRoleService_UpdateRolePermissions_NoPublisherIsAllowed(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	role := &model.Role{Name: "EmployeeAgent"}
	if err := roleRepo.Create(role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := permRepo.Create(&model.Permission{Code: "clients.read.all"}); err != nil {
		t.Fatalf("create perm: %v", err)
	}

	svc := NewRoleService(roleRepo, permRepo) // no publisher
	if err := svc.UpdateRolePermissions(role.ID, []string{"clients.read.all"}); err != nil {
		t.Fatalf("update: %v", err)
	}
}
