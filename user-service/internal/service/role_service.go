package service

import (
	"github.com/exbanka/user-service/internal/model"
)

// AllPermissions lists every permission code the system knows about.
var AllPermissions = []struct {
	Code     string
	Desc     string
	Category string
}{
	// Clients
	{"clients.create", "Create client profiles", "clients"},
	{"clients.read", "View client profiles", "clients"},
	{"clients.update", "Update client profiles", "clients"},
	// Accounts
	{"accounts.create", "Create bank accounts", "accounts"},
	{"accounts.read", "View bank accounts", "accounts"},
	{"accounts.update", "Update bank accounts", "accounts"},
	// Cards
	{"cards.create", "Create payment cards", "cards"},
	{"cards.read", "View payment cards", "cards"},
	{"cards.update", "Update/block/unblock cards", "cards"},
	{"cards.approve", "Approve card requests", "cards"},
	// Payments & Transfers
	{"payments.read", "View payments and transfers", "payments"},
	// Credits / Loans
	{"credits.read", "View loans and credit requests", "credits"},
	{"credits.approve", "Approve/reject loan requests", "credits"},
	// Securities
	{"securities.trade", "Execute securities trades", "securities"},
	{"securities.read", "View securities data", "securities"},
	// Employee management
	{"employees.create", "Create employees", "employees"},
	{"employees.update", "Update employees", "employees"},
	{"employees.read", "View employee profiles", "employees"},
	{"employees.permissions", "Manage employee permissions", "employees"},
	// Limits
	{"limits.manage", "Manage employee and client limits", "limits"},
	// Admin operations
	{"bank-accounts.manage", "Manage bank-owned accounts", "admin"},
	{"fees.manage", "Manage transfer fee rules", "admin"},
	{"interest-rates.manage", "Manage interest rate tiers and bank margins", "admin"},
	// Agent/OTC/Funds
	{"agents.manage", "Manage agent employees", "agents"},
	{"otc.manage", "Manage OTC trading", "otc"},
	{"funds.manage", "Manage investment funds", "funds"},
	// Stock trading operations
	{"orders.approve", "Approve/decline stock trading orders", "orders"},
	{"tax.manage", "Manage capital gains tax collection", "tax"},
	{"exchanges.manage", "Manage stock exchange settings and testing mode", "exchanges"},
	// Verification
	{"verification.skip", "Skip mobile verification for transactions", "verification"},
	{"verification.manage", "Manage verification settings per role", "verification"},
	// Securities administration
	{"securities.manage", "Manage securities data sources and market simulator settings", "securities"},
}

// DefaultRolePermissions defines seed data for roles inserted on first startup.
var DefaultRolePermissions = map[string][]string{
	"EmployeeBasic": {
		"clients.create", "clients.read", "clients.update",
		"accounts.create", "accounts.read", "accounts.update",
		"cards.create", "cards.read", "cards.update", "cards.approve",
		"payments.read",
		"credits.read", "credits.approve",
	},
	"EmployeeAgent": {
		"clients.create", "clients.read", "clients.update",
		"accounts.create", "accounts.read", "accounts.update",
		"cards.create", "cards.read", "cards.update", "cards.approve",
		"payments.read",
		"credits.read", "credits.approve",
		"securities.trade", "securities.read",
	},
	"EmployeeSupervisor": {
		"clients.create", "clients.read", "clients.update",
		"accounts.create", "accounts.read", "accounts.update",
		"cards.create", "cards.read", "cards.update", "cards.approve",
		"payments.read",
		"credits.read", "credits.approve",
		"securities.trade", "securities.read",
		"agents.manage", "otc.manage", "funds.manage",
		"orders.approve", "tax.manage", "exchanges.manage",
		"verification.skip", "verification.manage",
	},
	"EmployeeAdmin": {
		"clients.create", "clients.read", "clients.update",
		"accounts.create", "accounts.read", "accounts.update",
		"cards.create", "cards.read", "cards.update", "cards.approve",
		"payments.read",
		"credits.read", "credits.approve",
		"securities.trade", "securities.read",
		"agents.manage", "otc.manage", "funds.manage",
		"orders.approve", "tax.manage", "exchanges.manage",
		"employees.create", "employees.update",
		"employees.read", "employees.permissions",
		"limits.manage",
		"bank-accounts.manage", "fees.manage", "interest-rates.manage",
		"verification.skip", "verification.manage",
		"securities.manage",
	},
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
		perms, err := s.permRepo.ListByCodes(permCodes)
		if err != nil {
			return err
		}
		existing, err := s.roleRepo.GetByName(roleName)
		if err != nil {
			// Role doesn't exist yet — create it.
			if err2 := s.roleRepo.Create(&model.Role{
				Name:        roleName,
				Description: roleName + " default role",
				Permissions: perms,
			}); err2 != nil {
				return err2
			}
		} else {
			// Role already exists — sync its permissions so new permissions
			// added to DefaultRolePermissions take effect on restart.
			if err2 := s.roleRepo.SetPermissions(existing.ID, perms); err2 != nil {
				return err2
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

// GetRolesByNames fetches multiple roles by name.
func (s *RoleService) GetRolesByNames(names []string) ([]model.Role, error) {
	return s.roleRepo.GetByNames(names)
}

// GetPermissionsByCodes resolves permissions by their codes.
func (s *RoleService) GetPermissionsByCodes(codes []string) ([]model.Permission, error) {
	return s.permRepo.ListByCodes(codes)
}
