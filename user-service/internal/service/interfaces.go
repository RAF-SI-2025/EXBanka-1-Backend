// user-service/internal/service/interfaces.go
package service

import (
	"context"

	"github.com/exbanka/contract/changelog"
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

// ClientLimitClient is the subset of clientpb.ClientLimitServiceClient that
// BlueprintService needs. Defined as an interface to avoid tight coupling to
// the generated gRPC client and to enable unit testing with mocks.
type ClientLimitClient interface {
	SetClientLimits(ctx context.Context, clientID int64, dailyLimit, monthlyLimit, transferLimit string, setByEmployee int64) error
}
