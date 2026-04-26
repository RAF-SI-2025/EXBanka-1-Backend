package service

import (
	"context"
	"log"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/user-service/internal/model"
)

// AllPermissions lists every permission code the system knows about.
//
// Codes follow these conventions:
//   - <resource>.<action> for unique mutations (cards.block)
//   - <resource>.read.<scope> where scope is "all" / "own" / "bank" — the
//     handler reads which scope variant the caller holds and filters
//     results accordingly. Both .all and .own gate the same route via
//     RequireAnyPermission.
//   - <resource>.<sub>.<action> for resources with meaningful subtypes
//     (credits.approve.cash vs credits.approve.housing).
//
// Permissions never encode amount thresholds — EmployeeLimits gate that.
//
// Legacy coarse codes (clients.update, cards.update, etc.) remain in the
// catalog and remain granted to seed roles so existing routes continue
// to work until each route is converted to its granular replacement.
// They are NOT aliases at the middleware layer — every code stands alone.
// Once all routes use granular codes, the legacy entries can be removed.
var AllPermissions = []struct {
	Code     string
	Desc     string
	Category string
}{
	// ── Clients ─────────────────────────────────────────────────────
	{"clients.create", "Create client profiles", "clients"},
	{"clients.read", "View client profiles (legacy umbrella)", "clients"},
	{"clients.read.all", "View all client profiles", "clients"},
	{"clients.read.assigned", "View only assigned clients", "clients"},
	{"clients.update", "Update client profiles (legacy umbrella)", "clients"},
	{"clients.update.profile", "Update client profile fields", "clients"},
	{"clients.update.contact", "Update client contact details", "clients"},
	{"clients.update.limits", "Update client transaction limits", "clients"},
	{"clients.set-password", "Reset/change a client password", "clients"},
	{"clients.deactivate", "Deactivate a client account", "clients"},

	// ── Accounts ────────────────────────────────────────────────────
	{"accounts.create", "Create bank accounts (legacy umbrella)", "accounts"},
	{"accounts.create.current", "Open a current (RSD) account", "accounts"},
	{"accounts.create.foreign", "Open a foreign-currency account", "accounts"},
	{"accounts.read", "View accounts (legacy umbrella)", "accounts"},
	{"accounts.read.all", "View any account", "accounts"},
	{"accounts.read.own", "View only own accounts", "accounts"},
	{"accounts.update", "Update accounts (legacy umbrella)", "accounts"},
	{"accounts.update.name", "Rename an account", "accounts"},
	{"accounts.update.status", "Activate/freeze an account", "accounts"},
	{"accounts.update.limits", "Update account transaction limits", "accounts"},
	{"accounts.update.beneficiaries", "Manage account beneficiaries", "accounts"},
	{"accounts.deactivate", "Close/deactivate an account", "accounts"},

	// ── Bank-owned accounts (admin) ─────────────────────────────────
	{"bank-accounts.manage", "Manage bank-owned accounts (legacy umbrella)", "admin"},
	{"bank-accounts.create", "Open a bank-owned account", "admin"},
	{"bank-accounts.read", "View bank-owned accounts", "admin"},
	{"bank-accounts.update", "Update a bank-owned account", "admin"},
	{"bank-accounts.deactivate", "Deactivate a bank-owned account", "admin"},

	// ── Cards ───────────────────────────────────────────────────────
	{"cards.create", "Create cards (legacy umbrella)", "cards"},
	{"cards.create.physical", "Issue a physical card", "cards"},
	{"cards.create.virtual", "Issue a virtual card", "cards"},
	{"cards.read", "View cards (legacy umbrella)", "cards"},
	{"cards.read.all", "View any card", "cards"},
	{"cards.read.own", "View only own cards", "cards"},
	{"cards.update", "Update cards (legacy umbrella)", "cards"},
	{"cards.block", "Block (permanently) a card", "cards"},
	{"cards.unblock", "Unblock a card", "cards"},
	{"cards.set-temporary-block", "Apply a temporary block to a card", "cards"},
	{"cards.update-limits", "Update card spending limits", "cards"},
	{"cards.update-pin", "Update / reset card PIN", "cards"},
	{"cards.deactivate", "Deactivate a card", "cards"},
	{"cards.approve", "Approve card requests (legacy umbrella)", "cards"},
	{"cards.approve.physical", "Approve a physical-card request", "cards"},
	{"cards.approve.virtual", "Approve a virtual-card request", "cards"},
	{"cards.reject", "Reject a card request", "cards"},

	// ── Payments & Transfers ────────────────────────────────────────
	{"payments.read", "View payments (legacy umbrella)", "payments"},
	{"payments.read.all", "View any payment", "payments"},
	{"payments.read.own", "View only own payments", "payments"},
	{"transfers.read.all", "View any transfer", "payments"},
	{"transfers.read.own", "View only own transfers", "payments"},

	// ── Credits / Loans ─────────────────────────────────────────────
	{"credits.read", "View loans (legacy umbrella)", "credits"},
	{"credits.read.all", "View any loan", "credits"},
	{"credits.read.own", "View only own loans", "credits"},
	{"credits.approve", "Approve loans (legacy umbrella)", "credits"},
	{"credits.approve.cash", "Approve cash loans", "credits"},
	{"credits.approve.housing", "Approve housing loans", "credits"},
	{"credits.approve.auto", "Approve auto loans", "credits"},
	{"credits.approve.refinancing", "Approve refinancing loans", "credits"},
	{"credits.approve.student", "Approve student loans", "credits"},
	{"credits.reject", "Reject a loan request", "credits"},
	{"credits.disburse", "Disburse an approved loan", "credits"},

	// ── Securities (read) ───────────────────────────────────────────
	{"securities.read", "View securities (legacy umbrella)", "securities"},
	{"securities.read.catalog", "View the security catalog", "securities"},
	{"securities.read.market-data", "View live market data", "securities"},
	{"securities.read.holdings.all", "View any holdings portfolio", "securities"},
	{"securities.read.holdings.own", "View only own holdings", "securities"},
	{"securities.read.holdings.bank", "View the bank's portfolio", "securities"},

	// ── Securities (admin) ──────────────────────────────────────────
	{"securities.manage", "Manage securities sources (legacy umbrella)", "securities"},
	{"securities.manage.catalog", "Manage the security catalog", "securities"},
	{"securities.manage.market-simulator", "Manage market simulator settings", "securities"},
	{"securities.manage.cross-bank", "Manage cross-bank securities settings", "securities"},

	// ── Securities (trade — legacy coarse, now split per asset type) ─
	{"securities.trade", "Execute securities trades (legacy umbrella)", "securities"},

	// ── Orders ──────────────────────────────────────────────────────
	{"orders.read.all", "View any order", "orders"},
	{"orders.read.own", "View only own orders", "orders"},
	{"orders.read.bank", "View bank-owned orders", "orders"},
	{"orders.place.own", "Place an order for self (client)", "orders"},
	{"orders.place.on-behalf-client", "Place an order on behalf of a client", "orders"},
	{"orders.place.for-bank", "Place an order on behalf of the bank", "orders"},
	{"orders.place-on-behalf", "Place orders on behalf (legacy umbrella)", "orders"},
	{"orders.cancel.all", "Cancel any order", "orders"},
	{"orders.cancel.own", "Cancel own orders", "orders"},
	{"orders.approve", "Approve orders (legacy umbrella)", "orders"},
	{"orders.approve.market", "Approve market orders", "orders"},
	{"orders.approve.limit", "Approve limit orders", "orders"},
	{"orders.approve.stop", "Approve stop orders", "orders"},
	{"orders.reject", "Reject an order", "orders"},

	// ── OTC ─────────────────────────────────────────────────────────
	{"otc.manage", "Manage OTC trading (legacy umbrella)", "otc"},
	{"otc.trade.accept", "Accept an OTC offer", "otc"},
	{"otc.trade.exercise", "Exercise an OTC option", "otc"},
	{"otc.contracts.read.all", "View any OTC contract", "otc"},
	{"otc.contracts.read.own", "View only own OTC contracts", "otc"},
	{"otc.contracts.cancel", "Cancel an OTC contract", "otc"},
	{"otc.market-makers.manage", "Manage OTC market makers", "otc"},

	// ── Funds ───────────────────────────────────────────────────────
	{"funds.manage", "Manage funds (legacy umbrella)", "funds"},
	{"funds.create", "Create an investment fund", "funds"},
	{"funds.update", "Update an investment fund", "funds"},
	{"funds.suspend", "Suspend an investment fund", "funds"},
	{"funds.read.all", "View any investment fund", "funds"},
	{"funds.invest", "Invest in a fund", "funds"},
	{"funds.redeem", "Redeem from a fund", "funds"},

	// ── Forex ───────────────────────────────────────────────────────
	{"forex.trade.own", "Execute forex trades for self", "forex"},
	{"forex.trade.on-behalf-client", "Execute forex trades on behalf of a client", "forex"},
	{"forex.trade.for-bank", "Execute forex trades on behalf of the bank", "forex"},
	{"forex.read.all", "View any forex trade", "forex"},
	{"forex.read.own", "View only own forex trades", "forex"},

	// ── Employees ───────────────────────────────────────────────────
	{"employees.create", "Create employees", "employees"},
	{"employees.update", "Update employees (legacy umbrella)", "employees"},
	{"employees.update.profile", "Update employee profile fields", "employees"},
	{"employees.update.contact", "Update employee contact details", "employees"},
	{"employees.update.password", "Reset/change an employee password", "employees"},
	{"employees.deactivate", "Deactivate an employee", "employees"},
	{"employees.read", "View employees (legacy umbrella)", "employees"},
	{"employees.read.all", "View any employee profile", "employees"},
	{"employees.read.own", "View only own employee profile", "employees"},

	// ── Roles & Permissions ─────────────────────────────────────────
	{"employees.permissions", "Manage employee permissions (legacy umbrella)", "employees"},
	{"employees.roles.assign", "Assign roles to employees", "employees"},
	{"employees.permissions.assign", "Assign extra per-employee permissions", "employees"},
	{"roles.create", "Create a role", "employees"},
	{"roles.read", "View roles", "employees"},
	{"roles.update", "Update a role's permissions", "employees"},
	{"roles.delete", "Delete a role", "employees"},
	{"permissions.read", "List the permission catalog", "employees"},

	// ── Limits ──────────────────────────────────────────────────────
	{"limits.manage", "Manage limits (legacy umbrella)", "limits"},
	{"limits.employee.read", "View employee limits", "limits"},
	{"limits.employee.update", "Update employee limits", "limits"},
	{"limits.client.read", "View client limits", "limits"},
	{"limits.client.update", "Update client limits", "limits"},
	{"limit-templates.read", "View limit templates", "limits"},
	{"limit-templates.create", "Create a limit template", "limits"},
	{"limit-templates.update", "Update a limit template", "limits"},
	{"limit-templates.delete", "Delete a limit template", "limits"},

	// ── Fees & Rates ────────────────────────────────────────────────
	{"fees.manage", "Manage fees (legacy umbrella)", "admin"},
	{"fees.read", "View transfer fee rules", "admin"},
	{"fees.create", "Create a transfer fee rule", "admin"},
	{"fees.update", "Update a transfer fee rule", "admin"},
	{"fees.delete", "Delete a transfer fee rule", "admin"},
	{"interest-rates.manage", "Manage interest rates (legacy umbrella)", "admin"},
	{"interest-rates.tiers.read", "View interest-rate tiers", "admin"},
	{"interest-rates.tiers.update", "Update interest-rate tiers", "admin"},
	{"interest-rates.bank-margins.read", "View bank margins", "admin"},
	{"interest-rates.bank-margins.update", "Update bank margins", "admin"},

	// ── Agents / Tax / Exchanges ────────────────────────────────────
	{"agents.manage", "Manage agents (legacy umbrella)", "agents"},
	{"agents.read", "View agents", "agents"},
	{"agents.assign", "Assign an agent to a client", "agents"},
	{"agents.unassign", "Unassign an agent from a client", "agents"},
	{"tax.manage", "Manage tax (legacy umbrella)", "tax"},
	{"tax.read", "View tax records", "tax"},
	{"tax.collect", "Trigger tax collection", "tax"},
	{"tax.adjust", "Adjust tax records", "tax"},
	{"exchanges.manage", "Manage stock exchange settings (legacy umbrella)", "exchanges"},
	{"exchanges.read", "View stock exchange settings", "exchanges"},
	{"exchanges.update", "Update stock exchange settings", "exchanges"},
	{"exchanges.testing-mode.toggle", "Toggle testing mode for the simulator", "exchanges"},

	// ── Verification ────────────────────────────────────────────────
	{"verification.skip", "Skip mobile verification (legacy umbrella)", "verification"},
	{"verification.skip.transaction", "Skip verification for transactions", "verification"},
	{"verification.skip.password-change", "Skip verification for password change", "verification"},
	{"verification.skip.profile-update", "Skip verification for profile update", "verification"},
	{"verification.manage", "Manage verification (legacy umbrella)", "verification"},
	{"verification.policies.read", "View verification policies", "verification"},
	{"verification.policies.update", "Update verification policies", "verification"},
	{"verification.methods.update", "Update accepted verification methods", "verification"},

	// ── Changelog ───────────────────────────────────────────────────
	{"changelog.read.accounts", "View account changelog", "changelog"},
	{"changelog.read.employees", "View employee changelog", "changelog"},
	{"changelog.read.clients", "View client changelog", "changelog"},
	{"changelog.read.cards", "View card changelog", "changelog"},
	{"changelog.read.loans", "View loan changelog", "changelog"},
	{"changelog.read.orders", "View order changelog", "changelog"},
}

// DefaultRolePermissions defines the seed permission grants per role.
//
// Each role grants both legacy umbrella codes (still required by routes
// that have not yet been converted) AND the granular replacements (so
// future routes that use granular codes work). Once every route is
// converted to granular codes the legacy entries can be dropped.
//
// Hierarchy: Basic ⊂ Agent ⊂ Supervisor ⊂ Admin. Helpers below build
// each set incrementally so the inheritance is explicit.
var DefaultRolePermissions = map[string][]string{
	"EmployeeBasic":      basicPermissions(),
	"EmployeeAgent":      agentPermissions(),
	"EmployeeSupervisor": supervisorPermissions(),
	"EmployeeAdmin":      adminPermissions(),
}

// basicPermissions: client-facing tellers — manage clients, accounts,
// cards, view payments and loans, approve loans (subject to limits).
func basicPermissions() []string {
	return []string{
		// clients
		"clients.create", "clients.read", "clients.read.all", "clients.read.assigned",
		"clients.update", "clients.update.profile", "clients.update.contact", "clients.update.limits",
		"clients.set-password", "clients.deactivate",
		// accounts
		"accounts.create", "accounts.create.current", "accounts.create.foreign",
		"accounts.read", "accounts.read.all",
		"accounts.update", "accounts.update.name", "accounts.update.status",
		"accounts.update.limits", "accounts.update.beneficiaries", "accounts.deactivate",
		// cards
		"cards.create", "cards.create.physical", "cards.create.virtual",
		"cards.read", "cards.read.all",
		"cards.update", "cards.block", "cards.unblock", "cards.set-temporary-block",
		"cards.update-limits", "cards.update-pin", "cards.deactivate",
		"cards.approve", "cards.approve.physical", "cards.approve.virtual", "cards.reject",
		// payments
		"payments.read", "payments.read.all", "transfers.read.all",
		// credits
		"credits.read", "credits.read.all",
		"credits.approve",
		"credits.approve.cash", "credits.approve.housing", "credits.approve.auto",
		"credits.approve.refinancing", "credits.approve.student", "credits.reject",
		"credits.disburse",
		// changelog (read-only audit)
		"changelog.read.accounts", "changelog.read.clients", "changelog.read.cards",
		"changelog.read.loans",
	}
}

// agentPermissions: Basic + securities trading (for bank or on-behalf).
func agentPermissions() []string {
	return append(basicPermissions(),
		// securities read
		"securities.read", "securities.read.catalog", "securities.read.market-data",
		"securities.read.holdings.all", "securities.read.holdings.bank",
		// orders & forex (agent acts for bank or for client; never for self)
		"orders.read.all", "orders.read.bank",
		"orders.place.on-behalf-client", "orders.place.for-bank",
		"orders.place-on-behalf",
		"orders.cancel.all",
		"forex.trade.on-behalf-client", "forex.trade.for-bank",
		"forex.read.all",
		// trade umbrella (legacy)
		"securities.trade",
		// funds (agent invests/redeems for bank or on-behalf)
		"funds.invest", "funds.redeem", "funds.read.all",
		// otc (accept/exercise on behalf)
		"otc.trade.accept", "otc.trade.exercise",
		"otc.contracts.read.all",
		// changelog
		"changelog.read.orders",
	)
}

// supervisorPermissions: Agent + management of agents, OTC, funds,
// orders approval, tax, exchanges, verification.
func supervisorPermissions() []string {
	return append(agentPermissions(),
		"agents.manage", "agents.read", "agents.assign", "agents.unassign",
		"otc.manage", "otc.contracts.cancel", "otc.market-makers.manage",
		"funds.manage", "funds.create", "funds.update", "funds.suspend",
		"orders.approve", "orders.approve.market", "orders.approve.limit", "orders.approve.stop",
		"orders.reject",
		"tax.manage", "tax.read", "tax.collect", "tax.adjust",
		"exchanges.manage", "exchanges.read", "exchanges.update", "exchanges.testing-mode.toggle",
		"verification.skip",
		"verification.skip.transaction", "verification.skip.password-change", "verification.skip.profile-update",
		"verification.manage",
		"verification.policies.read", "verification.policies.update", "verification.methods.update",
	)
}

// adminPermissions: Supervisor + employee management + bank-owned
// accounts + fees + interest rates + securities admin + limits +
// limit-templates + permission/role management.
func adminPermissions() []string {
	return append(supervisorPermissions(),
		"employees.create",
		"employees.update", "employees.update.profile", "employees.update.contact",
		"employees.update.password", "employees.deactivate",
		"employees.read", "employees.read.all",
		"employees.permissions",
		"employees.roles.assign", "employees.permissions.assign",
		"roles.create", "roles.read", "roles.update", "roles.delete",
		"permissions.read",
		"limits.manage",
		"limits.employee.read", "limits.employee.update",
		"limits.client.read", "limits.client.update",
		"limit-templates.read", "limit-templates.create",
		"limit-templates.update", "limit-templates.delete",
		"bank-accounts.manage", "bank-accounts.create", "bank-accounts.read",
		"bank-accounts.update", "bank-accounts.deactivate",
		"fees.manage", "fees.read", "fees.create", "fees.update", "fees.delete",
		"interest-rates.manage",
		"interest-rates.tiers.read", "interest-rates.tiers.update",
		"interest-rates.bank-margins.read", "interest-rates.bank-margins.update",
		"securities.manage",
		"securities.manage.catalog", "securities.manage.market-simulator",
		"securities.manage.cross-bank",
		"changelog.read.employees",
	)
}

type RoleService struct {
	roleRepo  RoleRepo
	permRepo  PermissionRepo
	publisher RolePermPublisher
}

func NewRoleService(roleRepo RoleRepo, permRepo PermissionRepo) *RoleService {
	return &RoleService{roleRepo: roleRepo, permRepo: permRepo}
}

// WithPublisher wires the Kafka publisher used to emit
// role-permissions-changed events. Returns the same receiver mutated for
// builder-style wiring in cmd/main.go. Pass nil to disable publishing.
func (s *RoleService) WithPublisher(p RolePermPublisher) *RoleService {
	s.publisher = p
	return s
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

// CreateRole creates a new role with the given permission codes. Publishes a
// best-effort role-permissions-changed event so any pre-existing employees
// added to this role mid-flight (rare but possible) get session revocation.
func (s *RoleService) CreateRole(name, description string, permissionCodes []string) (*model.Role, error) {
	perms, err := s.permRepo.ListByCodes(permissionCodes)
	if err != nil {
		return nil, err
	}
	role := &model.Role{Name: name, Description: description, Permissions: perms}
	if err := s.roleRepo.Create(role); err != nil {
		return nil, err
	}
	s.publishRolePermissionsChanged(role.ID, name, "create_role")
	return role, nil
}

// UpdateRolePermissions replaces the permissions on a role and publishes a
// best-effort Kafka event so auth-service can revoke active sessions for every
// employee currently holding the role. A Kafka failure is logged but does NOT
// fail the update; the DB write is the source of truth.
func (s *RoleService) UpdateRolePermissions(roleID int64, permissionCodes []string) error {
	perms, err := s.permRepo.ListByCodes(permissionCodes)
	if err != nil {
		return err
	}
	if err := s.roleRepo.SetPermissions(roleID, perms); err != nil {
		return err
	}
	role, err := s.roleRepo.GetByID(roleID)
	roleName := ""
	if err == nil && role != nil {
		roleName = role.Name
	}
	s.publishRolePermissionsChanged(roleID, roleName, "update_role_permissions")
	return nil
}

// publishRolePermissionsChanged fans out the event. Nil publisher → no-op.
// Errors are logged at warn but never propagated; the DB state is canonical.
func (s *RoleService) publishRolePermissionsChanged(roleID int64, roleName, source string) {
	if s.publisher == nil {
		return
	}
	ids, err := s.roleRepo.ListEmployeeIDsByRole(roleID)
	if err != nil {
		log.Printf("WARN: role-perm-changed: list employees for role %d: %v", roleID, err)
		return
	}
	if len(ids) == 0 {
		// Nobody to revoke — skip the publish to keep the topic quiet.
		// Newly-created roles always hit this path because employees are
		// attached afterwards via SetEmployeeRoles.
		return
	}
	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              roleID,
		RoleName:            roleName,
		AffectedEmployeeIDs: ids,
		ChangedAt:           time.Now().Unix(),
		Source:              source,
	}
	if err := s.publisher.PublishRolePermissionsChanged(context.Background(), msg); err != nil {
		log.Printf("WARN: role-perm-changed: publish for role %d: %v", roleID, err)
	}
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
