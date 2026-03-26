package model

import "time"

// ActiveSession tracks where a user is logged in from.
type ActiveSession struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	UserID       int64      `gorm:"not null;index:idx_session_user"`
	UserRole     string     `gorm:"size:30;not null"` // employee role or "client"
	IPAddress    string     `gorm:"size:45"`
	UserAgent    string     `gorm:"size:512"`
	LastActiveAt time.Time  `gorm:"not null"`
	CreatedAt    time.Time  `gorm:"not null"`
	RevokedAt    *time.Time
}
