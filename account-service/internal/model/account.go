package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type Account struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber    string          `gorm:"uniqueIndex;size:18;not null"`
	AccountName      string          `gorm:"size:255"`
	OwnerID          uint64          `gorm:"not null;index:idx_account_owner"`
	OwnerName        string          `gorm:"size:255"`
	Balance          decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	AvailableBalance decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	EmployeeID       uint64          `gorm:"not null;index"`
	ExpiresAt        time.Time       `gorm:"not null"`
	CurrencyCode     string          `gorm:"size:3;not null;index:idx_account_currency"`
	Status           string          `gorm:"size:20;not null;default:'active';index:idx_account_status"`
	AccountKind      string          `gorm:"size:20;not null"`
	AccountType      string          `gorm:"size:20;not null;default:'standard'"`
	AccountCategory  string          `gorm:"size:50"`
	MaintenanceFee   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	DailyLimit       decimal.Decimal `gorm:"type:numeric(18,4);not null;default:1000000"`
	MonthlyLimit     decimal.Decimal `gorm:"type:numeric(18,4);not null;default:10000000"`
	DailySpending    decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MonthlySpending  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	CompanyID        *uint64         `gorm:"index"`
	Version          int64           `gorm:"not null;default:1"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        gorm.DeletedAt  `gorm:"index"`
}
