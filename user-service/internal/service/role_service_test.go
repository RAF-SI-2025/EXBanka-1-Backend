// user-service/internal/service/role_service_test.go
package service

import (
	"errors"
	"testing"

	"github.com/exbanka/user-service/internal/model"
	"github.com/stretchr/testify/assert"
)

// mockRoleRepo implements RoleRepo for testing.
type mockRoleRepo struct {
	roles map[string]*model.Role
	byID  map[int64]*model.Role
	nextID int64
}

func newMockRoleRepo() *mockRoleRepo {
	return &mockRoleRepo{
		roles:  make(map[string]*model.Role),
		byID:   make(map[int64]*model.Role),
		nextID: 1,
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

func TestRoleService_SeedRolesAndPermissions(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	err := svc.SeedRolesAndPermissions()
	assert.NoError(t, err)

	// Check some permissions were created
	p, err := permRepo.GetByCode("clients.read")
	assert.NoError(t, err)
	assert.Equal(t, "clients.read", p.Code)

	// Check roles were created
	r, err := roleRepo.GetByName("EmployeeBasic")
	assert.NoError(t, err)
	assert.Equal(t, "EmployeeBasic", r.Name)

	adminRole, err := roleRepo.GetByName("EmployeeAdmin")
	assert.NoError(t, err)
	assert.NotNil(t, adminRole)

	// Seeding again should be idempotent (no errors)
	err = svc.SeedRolesAndPermissions()
	assert.NoError(t, err)
}

func TestRoleService_GetPermissionsForRoles(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	// Seed
	_ = svc.SeedRolesAndPermissions()

	codes, err := svc.GetPermissionsForRoles([]string{"EmployeeBasic"})
	assert.NoError(t, err)
	assert.Contains(t, codes, "clients.read")
	assert.Contains(t, codes, "accounts.read")

	// Admin should have all permissions
	adminCodes, err := svc.GetPermissionsForRoles([]string{"EmployeeAdmin"})
	assert.NoError(t, err)
	assert.Contains(t, adminCodes, "employees.permissions")
	assert.Contains(t, adminCodes, "limits.manage")
}

func TestRoleService_CreateRole(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	// Add some permissions first
	_ = permRepo.Create(&model.Permission{Code: "clients.read", Category: "clients"})
	_ = permRepo.Create(&model.Permission{Code: "accounts.read", Category: "accounts"})

	role, err := svc.CreateRole("TestRole", "A test role", []string{"clients.read", "accounts.read"})
	assert.NoError(t, err)
	assert.Equal(t, "TestRole", role.Name)
	assert.Len(t, role.Permissions, 2)
}

func TestRoleService_UpdateRolePermissions(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	_ = permRepo.Create(&model.Permission{Code: "clients.read"})
	_ = permRepo.Create(&model.Permission{Code: "accounts.read"})
	_ = roleRepo.Create(&model.Role{ID: 1, Name: "TestRole"})

	err := svc.UpdateRolePermissions(1, []string{"clients.read", "accounts.read"})
	assert.NoError(t, err)

	role, _ := roleRepo.GetByID(1)
	assert.Len(t, role.Permissions, 2)
}

func TestRoleService_ListPermissions(t *testing.T) {
	roleRepo := newMockRoleRepo()
	permRepo := newMockPermRepo()
	svc := NewRoleService(roleRepo, permRepo)

	_ = svc.SeedRolesAndPermissions()

	perms, err := svc.ListPermissions()
	assert.NoError(t, err)
	assert.NotEmpty(t, perms)
	// Should have all 15 permission codes from AllPermissions
	assert.Equal(t, len(AllPermissions), len(perms))
}
