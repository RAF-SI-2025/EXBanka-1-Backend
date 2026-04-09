package model

import "time"

const (
	AccountStatusPending  = "pending"
	AccountStatusActive   = "active"
	AccountStatusDisabled = "disabled"
)

const (
	PrincipalTypeEmployee = "employee"
	PrincipalTypeClient   = "client"
)

type Account struct {
	ID            int64  `gorm:"primaryKey;autoIncrement"`
	Email         string `gorm:"uniqueIndex;not null"`
	PasswordHash  string `gorm:"not null;default:''"`
	Status        string `gorm:"size:20;not null;default:'pending'"`
	PrincipalType string `gorm:"size:20;not null"`
	PrincipalID   int64  `gorm:"not null"`
	MFAEnabled    bool   `gorm:"default:false"`
	CreatedAt     time.Time
}
