package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	SagaStatusPending      = "pending"
	SagaStatusCompleted    = "completed"
	SagaStatusFailed       = "failed"
	SagaStatusCompensating = "compensating"
	SagaStatusCompensated  = "compensated"
)

// SagaLog mirrors transaction-service/internal/model/saga_log.go. Stock-service
// uses it for two saga types:
//
//   - Placement saga (one per CreateOrder): validate_listing → ... →
//     reserve_funds → persist_order. Scoped to order_id.
//   - Fill saga (one per partial fill): record_transaction → convert_amount →
//     settle_reservation → update_holding → credit_commission → publish_kafka.
//     Scoped to order_id + order_transaction_id.
//
// Each step records pending → completed/failed. Compensating steps set
// IsCompensation=true with CompensationOf pointing at the forward step.
type SagaLog struct {
	ID                 uint64           `gorm:"primaryKey" json:"id"`
	SagaID             string           `gorm:"size:36;not null;index" json:"saga_id"`
	OrderID            uint64           `gorm:"not null;index" json:"order_id"`
	OrderTransactionID *uint64          `gorm:"index" json:"order_transaction_id,omitempty"`
	StepNumber         int              `gorm:"not null" json:"step_number"`
	StepName           string           `gorm:"size:64;not null" json:"step_name"`
	Status             string           `gorm:"size:16;not null;index" json:"status"`
	IsCompensation     bool             `gorm:"not null;default:false" json:"is_compensation"`
	CompensationOf     *uint64          `json:"compensation_of,omitempty"`
	Amount             *decimal.Decimal `gorm:"type:numeric(18,4)" json:"amount,omitempty"`
	CurrencyCode       string           `gorm:"size:3" json:"currency_code,omitempty"`
	Payload            datatypes.JSON   `json:"payload,omitempty"`
	ErrorMessage       string           `gorm:"type:text" json:"error_message,omitempty"`
	RetryCount         int              `gorm:"not null;default:0" json:"retry_count"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	Version            int64            `gorm:"not null;default:0" json:"-"`
}

func (SagaLog) TableName() string { return "saga_logs" }

// BeforeUpdate enforces optimistic locking via the Version column.
func (l *SagaLog) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", l.Version)
	l.Version++
	return nil
}
