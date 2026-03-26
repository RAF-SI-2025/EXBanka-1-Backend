package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// LedgerEntry records every balance change (double-entry bookkeeping style).
type LedgerEntry struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber string          `gorm:"not null;index:idx_ledger_account"`
	EntryType     string          `gorm:"not null"` // "debit" or "credit"
	Amount        decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	BalanceBefore decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	BalanceAfter  decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	Description   string          `gorm:"size:512"`
	ReferenceID   string          `gorm:"size:128;index"` // payment/transfer ID
	ReferenceType string          `gorm:"size:50"`        // "payment", "transfer", "fee", "interest"
	CreatedAt     time.Time       `gorm:"not null;index:idx_ledger_created"`
}
