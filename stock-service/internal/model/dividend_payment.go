package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// DividendPayment records a declared dividend for a security.
// One row per (security_id, payment_date); duplicate declarations are idempotent.
// Status lifecycle: declared → paid_out (or cancelled).
type DividendPayment struct {
	ID                   uint64          `gorm:"primaryKey;autoIncrement"`
	SecurityID           uint64          `gorm:"not null;uniqueIndex:idx_dp_sec_date"`
	Ticker               string          `gorm:"size:30;not null;index"`
	AmountPerShareRSD    decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	PaymentDate          time.Time       `gorm:"type:date;not null;uniqueIndex:idx_dp_sec_date"`
	Status               string          `gorm:"size:20;not null;default:'declared'"` // declared|paid_out|cancelled
	DeclaredByEmployeeID int64           `gorm:"not null"`
	PaidOutAt            *time.Time
	CreatedAt            time.Time
}

// BeforeCreate enforces a sensible default status.
func (d *DividendPayment) BeforeCreate(_ *gorm.DB) error {
	if d.Status == "" {
		d.Status = "declared"
	}
	return nil
}
