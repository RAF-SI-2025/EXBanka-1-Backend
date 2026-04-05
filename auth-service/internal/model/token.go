package model

import "time"

type RefreshToken struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	AccountID  int64     `gorm:"not null;index"`
	Token      string    `gorm:"uniqueIndex;not null"`
	ExpiresAt  time.Time `gorm:"not null"`
	Revoked    bool      `gorm:"default:false"`
	SystemType string    `gorm:"size:20;not null;default:'employee'"`
	SessionID  *int64    `gorm:"index"`    // FK to ActiveSession
	IPAddress  string    `gorm:"size:45"`  // IP from gateway metadata
	UserAgent  string    `gorm:"size:512"` // User-Agent from gateway metadata
	CreatedAt  time.Time
}

type ActivationToken struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	AccountID int64     `gorm:"not null;index"`
	Token     string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Used      bool      `gorm:"default:false"`
	CreatedAt time.Time
}

type PasswordResetToken struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	AccountID int64     `gorm:"not null;index"`
	Token     string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Used      bool      `gorm:"default:false"`
	CreatedAt time.Time
}
