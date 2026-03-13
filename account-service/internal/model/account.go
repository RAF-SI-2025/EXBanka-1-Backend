package model

import "time"

type Account struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountNumber    string    `gorm:"uniqueIndex;not null" json:"account_number"`
	AccountName      string    `json:"account_name"`
	OwnerID          uint64    `gorm:"not null" json:"owner_id"`
	OwnerName        string    `json:"owner_name"`
	Balance          float64   `gorm:"default:0" json:"balance"`
	AvailableBalance float64   `gorm:"default:0" json:"available_balance"`
	EmployeeID       uint64    `json:"employee_id"`
	ExpiresAt        time.Time `json:"expires_at"`
	CurrencyCode     string    `gorm:"size:3;not null" json:"currency_code"`
	Status           string    `gorm:"size:20;default:'active'" json:"status"`
	AccountKind      string    `gorm:"size:20;not null" json:"account_kind"`
	AccountType      string    `gorm:"size:30;not null" json:"account_type"`
	AccountCategory  string    `gorm:"size:20;not null" json:"account_category"`
	MaintenanceFee   float64   `gorm:"default:0" json:"maintenance_fee"`
	DailyLimit       float64   `gorm:"default:1000000" json:"daily_limit"`
	MonthlyLimit     float64   `gorm:"default:10000000" json:"monthly_limit"`
	DailySpending    float64   `gorm:"default:0" json:"daily_spending"`
	MonthlySpending  float64   `gorm:"default:0" json:"monthly_spending"`
	CompanyID        *uint64   `json:"company_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
