package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// EmployeeLimitReplica is a local read-model of an employee's limits, fed by
// user.employee-limits-updated events (SP-2). NON-AUTHORITATIVE — user-service
// owns employee limits. Used to avoid synchronous GetEmployeeLimits on the
// loan-approval gate.
type EmployeeLimitReplica struct {
	EmployeeID            uint64          `gorm:"primaryKey"` // == user-service Employee.ID
	MaxLoanApprovalAmount decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxSingleTransaction  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxDailyTransaction   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxClientDailyLimit   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MaxClientMonthlyLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	Version               int64           `gorm:"not null;default:0"` // source EmployeeLimit.Version; ordering guard
	UpdatedAt             time.Time
}
