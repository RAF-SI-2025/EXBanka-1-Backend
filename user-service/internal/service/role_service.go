package service

import (
	"context"
	"log"
	"sort"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/permissions"
	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
)

type RoleService struct {
	roleRepo  RoleRepo
	permRepo  PermissionRepo
	publisher RolePermPublisher
	db        *gorm.DB // optional; required only for SeedRolesAndPermissions
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

// WithDB wires a raw *gorm.DB handle used by SeedRolesAndPermissions to
// count rows in the join table and seed catalog rows transactionally —
// neither operation maps cleanly onto the narrow repository interfaces.
// Pass nil to disable seeding (in which case the seed becomes a no-op).
func (s *RoleService) WithDB(db *gorm.DB) *RoleService {
	s.db = db
	return s
}

// SeedRolesAndPermissions inserts the catalog's default role-permission
// mappings only on a fresh DB (no rows in role_permissions). After first
// startup, the DB is authoritative and admins manage mappings via the API.
//
// Permission catalog rows are upserted on every call (idempotent by code)
// so adding a new permission to the catalog still seeds the lookup row.
// Role↔permission mappings, however, are only seeded once.
func (s *RoleService) SeedRolesAndPermissions() error {
	if s.db == nil {
		// No DB handle wired (test/legacy mode). Nothing to do.
		return nil
	}

	// 1. Upsert the permission catalog (lookup table). Idempotent by code.
	for _, p := range permissions.Catalog {
		if _, err := s.permRepo.GetByCode(string(p)); err != nil {
			if err2 := s.permRepo.Create(&model.Permission{
				Code:        string(p),
				Description: string(p),
				Category:    permissionCategory(string(p)),
			}); err2 != nil {
				return err2
			}
		}
	}

	// 2. Skip role seeding if any role↔permission mapping already exists —
	// the admin (or a previous startup) is in charge from here on.
	var rpCount int64
	if err := s.db.Table("role_permissions").Count(&rpCount).Error; err != nil {
		return err
	}
	if rpCount > 0 {
		return nil
	}

	// 3. Fresh DB: insert the default role↔permission mappings from the
	// catalog. Iterate role names in sorted order so logs/tests are stable.
	roleNames := make([]string, 0, len(permissions.DefaultRoles))
	for name := range permissions.DefaultRoles {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)

	// Resolve every role's perm rows up front so the transaction below only
	// touches the tx connection — important for SQLite test DBs that pin
	// MaxOpenConns to 1.
	resolved := make(map[string][]model.Permission, len(roleNames))
	for _, roleName := range roleNames {
		perms := permissions.DefaultRoles[roleName]
		codes := make([]string, len(perms))
		for i, p := range perms {
			codes[i] = string(p)
		}
		permRows, err := s.permRepo.ListByCodes(codes)
		if err != nil {
			return err
		}
		resolved[roleName] = permRows
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, roleName := range roleNames {
			role := model.Role{Name: roleName, Description: roleName + " default role"}
			if err := tx.FirstOrCreate(&role, model.Role{Name: roleName}).Error; err != nil {
				return err
			}
			if err := tx.Model(&role).Association("Permissions").Replace(resolved[roleName]); err != nil {
				return err
			}
		}
		return nil
	})
}

// permissionCategory derives the catalog category for a permission from its
// first dotted segment (e.g. "clients.read.all" → "clients"). Falls back to
// "general" for malformed codes.
func permissionCategory(code string) string {
	for i := 0; i < len(code); i++ {
		if code[i] == '.' {
			return code[:i]
		}
	}
	return "general"
}

// AssignPermissionToRole grants a permission to a role. Validates against the
// codegened catalog — admins cannot grant a permission that does not exist.
// Idempotent: granting the same permission twice is a no-op.
func (s *RoleService) AssignPermissionToRole(roleID int64, perm string) error {
	if !permissions.IsValid(permissions.Permission(perm)) {
		return ErrPermissionNotInCatalog
	}
	role, err := s.roleRepo.GetByID(roleID)
	if err != nil {
		return ErrRoleNotFound
	}
	permRow, err := s.permRepo.GetByCode(perm)
	if err != nil {
		// Permission missing from the lookup table even though it's in the
		// catalog — create it on demand so the assign succeeds.
		permRow = &model.Permission{
			Code:        perm,
			Description: perm,
			Category:    permissionCategory(perm),
		}
		if err2 := s.permRepo.Create(permRow); err2 != nil {
			return err2
		}
	}
	merged := mergePermissions(role.Permissions, *permRow)
	if err := s.roleRepo.SetPermissions(role.ID, merged); err != nil {
		return err
	}
	s.publishRolePermissionsChanged(role.ID, role.Name, "assign_permission_to_role")
	return nil
}

// RevokePermissionFromRole removes a permission grant from a role. Idempotent:
// revoking a permission that was never granted is a no-op.
func (s *RoleService) RevokePermissionFromRole(roleID int64, perm string) error {
	role, err := s.roleRepo.GetByID(roleID)
	if err != nil {
		return ErrRoleNotFound
	}
	filtered := make([]model.Permission, 0, len(role.Permissions))
	for _, p := range role.Permissions {
		if p.Code != perm {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == len(role.Permissions) {
		// Nothing to revoke — keep the call cheap and silent.
		return nil
	}
	if err := s.roleRepo.SetPermissions(role.ID, filtered); err != nil {
		return err
	}
	s.publishRolePermissionsChanged(role.ID, role.Name, "revoke_permission_from_role")
	return nil
}

// mergePermissions returns the union of existing role permissions plus add,
// preserving existing order and appending new ones at the end. Deduplication
// is by Code.
func mergePermissions(existing []model.Permission, add model.Permission) []model.Permission {
	for _, p := range existing {
		if p.Code == add.Code {
			return existing
		}
	}
	return append(existing, add)
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
