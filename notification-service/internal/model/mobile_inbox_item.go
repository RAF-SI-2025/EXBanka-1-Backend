package model

import (
	"time"

	"gorm.io/datatypes"
)

// MobileInboxItem stores a pending mobile notification for a specific device.
// Items auto-expire and are cleaned up by a background goroutine.
type MobileInboxItem struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64         `gorm:"not null;index" json:"user_id"`
	DeviceID    string         `gorm:"size:36;not null;index" json:"device_id"`
	ChallengeID uint64         `gorm:"not null" json:"challenge_id"`
	Method      string         `gorm:"size:20;not null" json:"method"` // "code_pull", "qr_scan", "number_match"
	DisplayData datatypes.JSON `gorm:"type:jsonb" json:"display_data"`
	Status      string         `gorm:"size:20;not null;default:'pending'" json:"status"` // "pending", "delivered", "expired"
	ExpiresAt   time.Time      `gorm:"not null;index" json:"expires_at"`
	DeliveredAt *time.Time     `json:"delivered_at"`
	CreatedAt   time.Time      `json:"created_at"`
}
