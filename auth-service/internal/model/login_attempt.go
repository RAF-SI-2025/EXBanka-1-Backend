package model

import "time"

// LoginAttempt tracks each login attempt for brute-force detection.
type LoginAttempt struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Email     string    `gorm:"not null;index:idx_login_attempt_email"`
	IPAddress string    `gorm:"size:45"`
	Success   bool      `gorm:"not null;default:false"`
	CreatedAt time.Time `gorm:"not null;index:idx_login_attempt_created"`
}

// AccountLock tracks locked accounts after too many failed attempts.
type AccountLock struct {
	ID         int64      `gorm:"primaryKey;autoIncrement"`
	Email      string     `gorm:"not null;index"`
	Reason     string     `gorm:"not null;default:'too_many_failed_attempts'"`
	LockedAt   time.Time  `gorm:"not null"`
	ExpiresAt  time.Time  `gorm:"not null"`
	UnlockedAt *time.Time
}
