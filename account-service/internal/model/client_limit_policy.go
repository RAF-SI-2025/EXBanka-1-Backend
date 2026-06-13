package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ClientLimitPolicy is a local read-model of a client's limit policy, fed by
// client.limits-updated events (SP-5). NON-AUTHORITATIVE — client-service owns
// client limits. account-service uses it to propagate the policy to the client's
// per-account DailyLimit/MonthlyLimit caps. TransferLimit is NOT stored here
// (accounts have no transfer-limit column).
type ClientLimitPolicy struct {
	ClientID     uint64          `gorm:"primaryKey"` // == client-service Client.ID
	DailyLimit   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MonthlyLimit decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	Version      int64           `gorm:"not null;default:0"` // source ClientLimit.Version; ordering guard
	UpdatedAt    time.Time
}
