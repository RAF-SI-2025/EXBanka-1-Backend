package model

import (
	"time"

	"gorm.io/gorm"
)

// MobileDevice represents a registered mobile app instance for a user.
// One active device per user, enforced at service level.
type MobileDevice struct {
	ID                int64  `gorm:"primaryKey;autoIncrement"`
	UserID            int64  `gorm:"not null;index"`
	SystemType        string `gorm:"size:20;not null"` // "client" or "employee"
	DeviceID          string `gorm:"size:36;uniqueIndex;not null"`
	DeviceSecret      string `gorm:"size:64;not null" json:"-"`
	DeviceName        string `gorm:"size:100;not null"`
	Status            string `gorm:"size:20;not null;default:'pending'"` // "pending", "active", "deactivated"
	BiometricsEnabled bool   `gorm:"default:false"`
	ActivatedAt       *time.Time
	DeactivatedAt     *time.Time
	LastSeenAt        time.Time
	Version           int64 `gorm:"not null;default:1"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (d *MobileDevice) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", d.Version)
	d.Version++
	return nil
}

// MobileActivationCode is a short-lived 6-digit code sent to email for device activation.
type MobileActivationCode struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Email     string    `gorm:"not null;index"`
	Code      string    `gorm:"size:6;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Attempts  int       `gorm:"not null;default:0"`
	Used      bool      `gorm:"default:false"`
	CreatedAt time.Time
}
