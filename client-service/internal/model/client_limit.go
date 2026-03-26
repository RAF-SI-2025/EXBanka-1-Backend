package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ClientLimit defines the transaction limits for a client account.
// Limits are set by employees and constrained by the employee's own limits.
type ClientLimit struct {
	ID            int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID      int64           `gorm:"uniqueIndex;not null" json:"client_id"`
	DailyLimit    decimal.Decimal `gorm:"type:numeric(18,4);not null;default:100000" json:"daily_limit"`
	MonthlyLimit  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:1000000" json:"monthly_limit"`
	TransferLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:50000" json:"transfer_limit"`
	SetByEmployee int64           `gorm:"not null" json:"set_by_employee"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}
