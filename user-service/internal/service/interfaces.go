// user-service/internal/service/interfaces.go
package service

import "github.com/exbanka/user-service/internal/model"

type EmployeeRepo interface {
	Create(emp *model.Employee) error
	GetByID(id int64) (*model.Employee, error)
	GetByEmail(email string) (*model.Employee, error)
	GetByJMBG(jmbg string) (*model.Employee, error)
	Update(emp *model.Employee) error
	SetPassword(userID int64, passwordHash string) error
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
