package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// BankOperation is an idempotency record for DebitBankAccount and CreditBankAccount
// on the bank sentinel accounts. The (reference, direction) composite is unique —
// a second apply with the same key returns the cached result instead of double-applying.
type BankOperation struct {
	ID            uint64          `gorm:"primaryKey"`
	Reference     string          `gorm:"size:128;not null;uniqueIndex:idx_bank_op_ref_dir"`
	Direction     string          `gorm:"size:16;not null;uniqueIndex:idx_bank_op_ref_dir"`
	Currency      string          `gorm:"size:8;not null"`
	Amount        decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	AccountNumber string          `gorm:"size:64;not null"`
	NewBalance    decimal.Decimal `gorm:"type:numeric(20,4);not null"`
	Reason        string          `gorm:"size:255"`
	CreatedAt     time.Time
}
