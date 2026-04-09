package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// SagaLog records each step of a multi-service saga (transfer or payment).
// Forward steps are persisted as "pending" → "completed" or "failed".
// Compensation steps are persisted as "compensating" → "completed" (if retry succeeds)
// or left in "compensating" for the recovery goroutine to retry.
type SagaLog struct {
	ID              uint64          `gorm:"primaryKey;autoIncrement"`
	SagaID          string          `gorm:"size:64;not null;index:idx_saga_id"`
	TransactionID   uint64          `gorm:"not null;index"`
	TransactionType string          `gorm:"size:20;not null"` // "transfer" or "payment"
	StepNumber      int             `gorm:"not null"`
	StepName        string          `gorm:"size:100;not null"`
	Status          string          `gorm:"size:20;not null;default:'pending'"` // pending, completed, failed, compensating, dead_letter
	IsCompensation  bool            `gorm:"not null;default:false"`
	AccountNumber   string          `gorm:"size:34;not null"`
	Amount          decimal.Decimal `gorm:"type:numeric(18,4);not null"` // signed: negative = debit, positive = credit
	ErrorMessage    string          `gorm:"size:512"`
	CompensationOf  *uint64         `gorm:"index"`              // points to the forward-step ID this compensates
	RetryCount      int             `gorm:"not null;default:0"` // incremented each time recovery retry fails
	CreatedAt       time.Time       `gorm:"not null"`
	CompletedAt     *time.Time
}
