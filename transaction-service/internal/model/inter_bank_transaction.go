package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// InterBankTransaction is the durable 2-phase-commit state for a single
// cross-bank transfer. One row per (tx_id, role); both the sender and the
// receiver hold their own row keyed by `role`.
//
// Column rename note: spec §4.2 keeps `fees_final` for inter-bank rather
// than the legacy `fees_rsd` from intra-bank — fees on inter-bank are stored
// in the receiver's currency (currency_final), not always in RSD.
type InterBankTransaction struct {
	TxID                  string           `gorm:"primaryKey;size:36"`
	Role                  string           `gorm:"primaryKey;size:10"`
	RemoteBankCode        string           `gorm:"size:3;not null;index"`
	SenderAccountNumber   string           `gorm:"size:32;not null"`
	ReceiverAccountNumber string           `gorm:"size:32;not null"`
	AmountNative          decimal.Decimal  `gorm:"type:numeric(20,4);not null"`
	CurrencyNative        string           `gorm:"size:3;not null"`
	AmountFinal           *decimal.Decimal `gorm:"type:numeric(20,4)"`
	CurrencyFinal         *string          `gorm:"size:3"`
	FxRate                *decimal.Decimal `gorm:"type:numeric(20,8)"`
	FeesFinal             *decimal.Decimal `gorm:"type:numeric(20,4);column:fees_final"`
	Phase                 string           `gorm:"size:16;not null"`
	Status                string           `gorm:"size:24;not null;index"`
	ErrorReason           string           `gorm:"type:text"`
	RetryCount            int              `gorm:"not null;default:0"`
	PayloadJSON           datatypes.JSON   `gorm:"type:jsonb"`
	IdempotencyKey        string           `gorm:"size:64;not null;uniqueIndex"`
	CreatedAt             time.Time        `gorm:"not null"`
	UpdatedAt             time.Time        `gorm:"not null;index"`
	Version               int64            `gorm:"not null;default:0"`
}

func (InterBankTransaction) TableName() string { return "inter_bank_transactions" }

// BeforeUpdate enforces optimistic locking via the Version column, matching
// the pattern used by other versioned models in this service.
func (t *InterBankTransaction) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", t.Version)
	}
	t.Version++
	return nil
}
