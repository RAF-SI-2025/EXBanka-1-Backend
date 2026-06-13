// user-service/internal/service/interfaces.go
package service

import (
	"context"

	"github.com/exbanka/contract/changelog"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/user-service/internal/model"
)

// ChangelogRepo is the interface for changelog persistence.
type ChangelogRepo interface {
	Create(entry changelog.Entry) error
	CreateBatch(entries []changelog.Entry) error
	ListByEntity(entityType string, entityID int64, page, pageSize int) ([]model.Changelog, int64, error)
}

type EmployeeRepo interface {
	Create(emp *model.Employee) error
	GetByID(id int64) (*model.Employee, error)
	GetByEmail(email string) (*model.Employee, error)
	GetByJMBG(jmbg string) (*model.Employee, error)
	Update(emp *model.Employee) error
	List(emailFilter, nameFilter, positionFilter string, page, pageSize int) ([]model.Employee, int64, error)
	GetByIDWithRoles(id int64) (*model.Employee, error)
	GetByEmailWithRoles(email string) (*model.Employee, error)
	SetEmployeeRoles(employeeID int64, roles []model.Role) error
	SetAdditionalPermissions(employeeID int64, perms []model.Permission) error
	GetByIDs(ids []int64) ([]model.Employee, error)
}

type RoleRepo interface {
	Create(role *model.Role) error
	GetByID(id int64) (*model.Role, error)
	GetByName(name string) (*model.Role, error)
	GetByNames(names []string) ([]model.Role, error)
	List() ([]model.Role, error)
	Update(role *model.Role) error
	SetPermissions(roleID int64, permissions []model.Permission) error
	Delete(id int64) error
	ListEmployeeIDsByRole(roleID int64) ([]int64, error)
}

// RolePermPublisher is the narrow Kafka surface RoleService needs.
type RolePermPublisher interface {
	PublishRolePermissionsChanged(ctx context.Context, msg kafkamsg.RolePermissionsChangedMessage) error
}

// EmployeeCacheEvictor is the slice of the Redis cache RoleService needs: it
// evicts an employee's cached record after a role-permission change so the next
// GetEmployee reflects the new permission set. *cache.RedisCache satisfies it;
// an interface so the eviction is unit-testable.
type EmployeeCacheEvictor interface {
	Delete(ctx context.Context, key string) error
}

// EmployeeEventPublisher is the narrow Kafka surface EmployeeService needs.
// An interface (not the concrete producer) so the publish paths — including the
// per-employee force-refresh via RolePermissionsChanged — are unit-testable.
type EmployeeEventPublisher interface {
	PublishEmployeeCreated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error
	PublishEmployeeUpdated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error
	PublishRolePermissionsChanged(ctx context.Context, msg kafkamsg.RolePermissionsChangedMessage) error
}

type PermissionRepo interface {
	Create(p *model.Permission) error
	GetByCode(code string) (*model.Permission, error)
	GetByID(id int64) (*model.Permission, error)
	List() ([]model.Permission, error)
	ListByCategory(category string) ([]model.Permission, error)
	ListByCodes(codes []string) ([]model.Permission, error)
	Delete(id int64) error
}

type EmployeeLimitRepo interface {
	Create(limit *model.EmployeeLimit) error
	GetByEmployeeID(employeeID int64) (*model.EmployeeLimit, error)
	Update(limit *model.EmployeeLimit) error
	Delete(employeeID int64) error
	Upsert(limit *model.EmployeeLimit) error
	ResetDailyUsedLimits() error
}

type LimitTemplateRepo interface {
	Create(t *model.LimitTemplate) error
	GetByID(id int64) (*model.LimitTemplate, error)
	GetByName(name string) (*model.LimitTemplate, error)
	List() ([]model.LimitTemplate, error)
	Update(t *model.LimitTemplate) error
	Delete(id int64) error
}

type ActuaryRepo interface {
	Create(limit *model.ActuaryLimit) error
	GetByID(id int64) (*model.ActuaryLimit, error)
	GetByEmployeeID(employeeID int64) (*model.ActuaryLimit, error)
	Save(limit *model.ActuaryLimit) error
	ListActuaries(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error)
	Upsert(limit *model.ActuaryLimit) error
}

type ActuaryEmpRepo interface {
	GetByIDWithRoles(id int64) (*model.Employee, error)
}

type LimitBlueprintRepo interface {
	Create(bp *model.LimitBlueprint) error
	GetByID(id uint64) (*model.LimitBlueprint, error)
	List(bpType string) ([]model.LimitBlueprint, error)
	GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error)
	Update(bp *model.LimitBlueprint) error
	Delete(id uint64) error
}

// LimitEventPublisher is the narrow Kafka surface LimitService needs.
// An interface (not the concrete producer) so publish paths are unit-testable.
type LimitEventPublisher interface {
	PublishEmployeeLimitsUpdated(ctx context.Context, msg kafkamsg.EmployeeLimitsUpdatedMessage) error
	PublishLimitTemplate(ctx context.Context, msg kafkamsg.LimitTemplateMessage) error
}
