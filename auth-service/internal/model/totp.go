package model

import "time"

// TOTPSecret stores the TOTP shared secret for a user's 2FA.
type TOTPSecret struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	UserID    int64  `gorm:"not null;uniqueIndex"`
	Secret    string `gorm:"not null"`               // base32-encoded TOTP secret
	Enabled   bool   `gorm:"not null;default:false"` // only true after first successful verification
	CreatedAt time.Time
	UpdatedAt time.Time
}
