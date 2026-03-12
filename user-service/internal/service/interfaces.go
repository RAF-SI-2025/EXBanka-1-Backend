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
}
