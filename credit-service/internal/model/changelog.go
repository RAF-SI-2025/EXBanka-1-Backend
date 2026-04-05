package model

import "time"

// Changelog is an append-only audit trail for entity mutations.
type Changelog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	EntityType string    `gorm:"size:64;not null;index:idx_changelog_entity"`
	EntityID   int64     `gorm:"not null;index:idx_changelog_entity"`
	Action     string    `gorm:"size:32;not null"`
	FieldName  string    `gorm:"size:128"`
	OldValue   string    `gorm:"type:text"`
	NewValue   string    `gorm:"type:text"`
	ChangedBy  int64     `gorm:"not null"`
	ChangedAt  time.Time `gorm:"not null;default:now()"`
	Reason     string    `gorm:"type:text"`
}

func (Changelog) TableName() string {
	return "changelogs"
}
