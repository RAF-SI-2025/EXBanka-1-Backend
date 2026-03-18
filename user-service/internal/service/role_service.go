package service

import (
	"github.com/exbanka/user-service/internal/model"
)

// DefaultRolePermissions defines seed data for roles inserted on first startup.
var DefaultRolePermissions = map[string][]string{
	"EmployeeBasic": {
		"clients.read", "accounts.create", "accounts.read",
		"cards.manage", "credits.manage",
	},
	"EmployeeAgent": {
		"clients.read", "accounts.create", "accounts.read",
		"cards.manage", "credits.manage",
		"securities.trade", "securities.read",
	},
	"EmployeeSupervisor": {
		"clients.read", "accounts.create", "accounts.read",
		"cards.manage", "credits.manage",
		"securities.trade", "securities.read",
		"agents.manage", "otc.manage", "funds.manage",
	},
	"EmployeeAdmin": {
		"clients.read", "accounts.create", "accounts.read",
		"cards.manage", "credits.manage",
		"securities.trade", "securities.read",
		"agents.manage", "otc.manage", "funds.manage",
		"employees.create", "employees.update",
		"employees.read", "employees.permissions",
		"limits.manage",
	},
}

// AllPermissions lists every permission code the system knows about.
var AllPermissions = []struct {
	Code     string
	Desc     string
	Category string
}{
	{"clients.read", "View client profiles", "clients"},
	{"accounts.create", "Create bank accounts", "accounts"},
	{"accounts.read", "View bank accounts", "accounts"},
	{"cards.manage", "Manage payment cards", "cards"},
	{"credits.manage", "Manage loans and credit requests", "credits"},
	{"securities.trade", "Execute securities trades", "securities"},
	{"securities.read", "View securities data", "securities"},
	{"agents.manage", "Manage agent employees", "agents"},
	{"otc.manage", "Manage OTC trading", "otc"},
	{"funds.manage", "Manage investment funds", "funds"},
	{"employees.create", "Create employees", "employees"},
	{"employees.update", "Update employees", "employees"},
	{"employees.read", "View employee profiles", "employees"},
	{"employees.permissions", "Manage employee permissions", "employees"},
	{"limits.manage", "Manage employee and client limits", "limits"},
}

type RoleService struct {
	roleRepo RoleRepo
	permRepo PermissionRepo
}

func NewRoleService(roleRepo RoleRepo, permRepo PermissionRepo) *RoleService {
	return &RoleService{roleRepo: roleRepo, permRepo: permRepo}
}

// SeedRolesAndPermissions inserts default permissions and roles if they don't exist.
func (s *RoleService) SeedRolesAndPermissions() error {
	for _, p := range AllPermissions {
		_, err := s.permRepo.GetByCode(p.Code)
		if err != nil {
			if err2 := s.permRepo.Create(&model.Permission{
				Code: p.Code, Description: p.Desc, Category: p.Category,
			}); err2 != nil {
				return err2
			}
		}
	}
	for roleName, permCodes := range DefaultRolePermissions {
		_, err := s.roleRepo.GetByName(roleName)
		if err != nil {
			perms, err2 := s.permRepo.ListByCodes(permCodes)
			if err2 != nil {
				return err2
			}
			if err3 := s.roleRepo.Create(&model.Role{
				Name:        roleName,
				Description: roleName + " default role",
				Permissions: perms,
			}); err3 != nil {
				return err3
			}
		}
	}
	return nil
}

// ValidRole checks if a role name exists in the DB.
func (s *RoleService) ValidRole(name string) bool {
	_, err := s.roleRepo.GetByName(name)
	return err == nil
}

// GetPermissionsForRoles resolves all unique permission codes for a set of role names.
func (s *RoleService) GetPermissionsForRoles(roleNames []string) ([]string, error) {
	roles, err := s.roleRepo.GetByNames(roleNames)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var codes []string
	for _, role := range roles {
		for _, perm := range role.Permissions {
			if !seen[perm.Code] {
				seen[perm.Code] = true
				codes = append(codes, perm.Code)
			}
		}
	}
	return codes, nil
}

// GetRole fetches a single role by ID.
func (s *RoleService) GetRole(id int64) (*model.Role, error) {
	return s.roleRepo.GetByID(id)
}

// ListRoles returns all roles with their permissions.
func (s *RoleService) ListRoles() ([]model.Role, error) {
	return s.roleRepo.List()
}

// CreateRole creates a new role with the given permission codes.
func (s *RoleService) CreateRole(name, description string, permissionCodes []string) (*model.Role, error) {
	perms, err := s.permRepo.ListByCodes(permissionCodes)
	if err != nil {
		return nil, err
	}
	role := &model.Role{Name: name, Description: description, Permissions: perms}
	if err := s.roleRepo.Create(role); err != nil {
		return nil, err
	}
	return role, nil
}

// UpdateRolePermissions replaces the permissions on a role.
func (s *RoleService) UpdateRolePermissions(roleID int64, permissionCodes []string) error {
	perms, err := s.permRepo.ListByCodes(permissionCodes)
	if err != nil {
		return err
	}
	return s.roleRepo.SetPermissions(roleID, perms)
}

// ListPermissions returns all permissions.
func (s *RoleService) ListPermissions() ([]model.Permission, error) {
	return s.permRepo.List()
}
