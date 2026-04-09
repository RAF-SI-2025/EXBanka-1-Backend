package model

import (
	"time"

	"gorm.io/datatypes"
)

// MobileInboxItem stores a pending verification notification for a user.
// Both browser-initiated and mobile-initiated challenges land here, queryable
// by user_id alone — no device filtering.
type MobileInboxItem struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64         `gorm:"not null;index" json:"user_id"`
	ChallengeID uint64         `gorm:"not null" json:"challenge_id"`
	Method      string         `gorm:"size:20;not null" json:"method"`
	DisplayData datatypes.JSON `gorm:"type:jsonb" json:"display_data"`
	Status      string         `gorm:"size:20;not null;default:'pending'" json:"status"`
	ExpiresAt   time.Time      `gorm:"not null;index" json:"expires_at"`
	DeliveredAt *time.Time     `json:"delivered_at"`
	CreatedAt   time.Time      `json:"created_at"`
}
