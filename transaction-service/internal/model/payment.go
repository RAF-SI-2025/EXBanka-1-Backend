package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type Payment struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement"`
	IdempotencyKey    string          `gorm:"uniqueIndex;size:36;not null"`
	FromAccountNumber string          `gorm:"not null;index:idx_payment_from"`
	ToAccountNumber   string          `gorm:"not null;index:idx_payment_to"`
	InitialAmount     decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	FinalAmount       decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	Commission        decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	CurrencyCode      string          `gorm:"size:3;not null;default:'RSD'"`
	RecipientName     string          `gorm:"size:255"`
	PaymentCode       string          `gorm:"size:10"`
	ReferenceNumber   string          `gorm:"size:50"`
	PaymentPurpose    string          `gorm:"size:255"`
	Status            string          `gorm:"size:20;not null;default:'pending';index:idx_payment_status"`
	FailureReason     string          `gorm:"size:512"`
	Version           int64           `gorm:"not null;default:1"`
	Timestamp         time.Time       `gorm:"not null;index:idx_payment_timestamp"`
	CompletedAt       *time.Time
}
