package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// BeforeUpdate adds a WHERE version=? clause and increments the version,
// providing optimistic locking on every Save/Update of a Transfer.
func (t *Transfer) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", t.Version)
	t.Version++
	return nil
}

type Transfer struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement"`
	IdempotencyKey    string          `gorm:"uniqueIndex;size:36;not null"`
	ClientID          uint64          `gorm:"not null;default:0;index:idx_transfer_client"`
	FromAccountNumber string          `gorm:"not null;index:idx_transfer_from"`
	ToAccountNumber   string          `gorm:"not null;index:idx_transfer_to"`
	InitialAmount     decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	FinalAmount       decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	ExchangeRate      decimal.Decimal `gorm:"type:numeric(18,8);not null;default:1"`
	Commission        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	FromCurrency      string          `gorm:"size:3;not null;default:'RSD'"`
	ToCurrency        string          `gorm:"size:3;not null;default:'RSD'"`
	Status            string          `gorm:"size:20;not null;default:'pending';index"`
	FailureReason     string          `gorm:"size:512"`
	Version           int64           `gorm:"not null;default:1"`
	Timestamp         time.Time       `gorm:"not null"`
	CompletedAt       *time.Time
}
