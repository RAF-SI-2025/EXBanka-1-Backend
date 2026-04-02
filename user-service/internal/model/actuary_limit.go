package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ActuaryLimit stores trading limits for employees with Agent or Supervisor roles.
// Supervisors always have NeedApproval=false and Limit is ignored for them.
type ActuaryLimit struct {
	ID           int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	EmployeeID   int64           `gorm:"uniqueIndex;not null" json:"employee_id"`
	Limit        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"limit"`
	UsedLimit    decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"used_limit"`
	NeedApproval bool            `gorm:"not null;default:false" json:"need_approval"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Version      int64           `gorm:"not null;default:1" json:"version"`
}

func (a *ActuaryLimit) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", a.Version)
	a.Version++
	return nil
}

// ActuaryRow is the result of the ListActuaries join query.
type ActuaryRow struct {
	EmployeeID        int64   `gorm:"column:employee_id"`
	FirstName         string  `gorm:"column:first_name"`
	LastName          string  `gorm:"column:last_name"`
	Email             string  `gorm:"column:email"`
	Position          string  `gorm:"column:position"`
	Limit             float64 `gorm:"column:limit"`
	UsedLimit         float64 `gorm:"column:used_limit"`
	NeedApproval      bool    `gorm:"column:need_approval"`
	ActuaryLimitIDPtr *int64  `gorm:"column:actuary_limit_id"`
}

// ActuaryLimitID returns the actuary_limit_id or 0 if nil.
func (r ActuaryRow) ActuaryLimitID() uint64 {
	if r.ActuaryLimitIDPtr != nil {
		return uint64(*r.ActuaryLimitIDPtr)
	}
	return 0
}
