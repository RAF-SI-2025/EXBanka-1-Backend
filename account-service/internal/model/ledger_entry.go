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
	// IdempotencyKey, when non-empty, is unique across ledger_entries. A
	// subsequent UpdateBalance call with the same key becomes a no-op and
	// returns the original response — used for safe crash-retry of saga
	// credit/debit steps (stock-service's SagaRecovery).
	//
	// The partial unique index (WHERE idempotency_key <> '') is supported by
	// both PostgreSQL (production) and the SQLite build used in tests
	// (modernc glebarez/sqlite). Pre-existing rows with an empty string are
	// untouched by the constraint.
	IdempotencyKey string `gorm:"size:128;uniqueIndex:idx_ledger_idempotency_key,where:idempotency_key <> ''" json:"idempotency_key,omitempty"`
	// SagaID and SagaStep are stamped onto rows created from inside a saga
	// step (read from context.Context via contract/shared/saga). They make
	// cross-service auditing trivial: SELECT * FROM ledger_entries WHERE
	// saga_id = '...'. Both are nullable: direct (non-saga) writes leave
	// them NULL.
	SagaID    *string   `gorm:"size:36;index" json:"saga_id,omitempty"`
	SagaStep  *string   `gorm:"size:64" json:"saga_step,omitempty"`
	CreatedAt time.Time `gorm:"not null;index:idx_ledger_created"`
}
