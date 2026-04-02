package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// VerificationChallenge stores the state of a single verification challenge
// (code_pull, qr_scan, number_match, or email fallback).
type VerificationChallenge struct {
	ID            uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        uint64         `gorm:"not null;index" json:"user_id"`
	SourceService string         `gorm:"size:30;not null" json:"source_service"` // "transaction", "payment", "transfer"
	SourceID      uint64         `gorm:"not null" json:"source_id"`
	Method        string         `gorm:"size:20;not null" json:"method"` // "code_pull", "qr_scan", "number_match", "email"
	Code          string         `gorm:"size:6;not null" json:"-"`       // 6-digit code (used for code_pull and email; internal for qr/number)
	ChallengeData datatypes.JSON `gorm:"type:jsonb" json:"challenge_data"`
	Status        string         `gorm:"size:20;not null;default:'pending';index" json:"status"` // "pending", "verified", "expired", "failed"
	Attempts      int            `gorm:"not null;default:0" json:"attempts"`
	ExpiresAt     time.Time      `gorm:"not null;index" json:"expires_at"`
	VerifiedAt    *time.Time     `json:"verified_at,omitempty"`
	DeviceID      string         `gorm:"size:100" json:"device_id,omitempty"` // filled when mobile app claims the challenge
	Version       int64          `gorm:"not null;default:1" json:"-"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// BeforeUpdate enforces optimistic locking via the Version field.
// The WHERE clause filters on the current version; if another transaction
// already incremented it, RowsAffected will be 0.
func (vc *VerificationChallenge) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", vc.Version)
	vc.Version++
	return nil
}
