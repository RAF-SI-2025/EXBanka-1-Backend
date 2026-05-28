package model

import "time"

// GeneralNotification is a persistent, user-visible notification.
// Unlike MobileInboxItem, it has no expiry and tracks read/unread status.
type GeneralNotification struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID         uint64    `gorm:"not null;index:idx_notif_user_read" json:"user_id"`
	Type           string    `gorm:"size:50;not null" json:"type"`
	Title          string    `gorm:"size:255;not null" json:"title"`
	Message        string    `gorm:"type:text;not null" json:"message"`
	IsRead         bool      `gorm:"not null;default:false;index:idx_notif_user_read" json:"is_read"`
	RefType        string    `gorm:"size:50" json:"ref_type,omitempty"`
	RefID          uint64    `json:"ref_id,omitempty"`
	// IdempotencyKey is an optional deduplication key. When non-empty the
	// repository uses a partial unique index (only rows where the key is
	// non-empty) to prevent duplicate inserts on cron restart.
	// Format: <consumer>-<uid>-<ticker/ref>-<YYYYMMDD>
	IdempotencyKey string    `gorm:"size:128;default:''" json:"idempotency_key,omitempty"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
}
