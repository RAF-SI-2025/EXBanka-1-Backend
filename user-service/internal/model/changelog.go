package model

import "time"

// Changelog is an append-only audit trail for entity mutations.
// Each service has its own changelogs table in its own database.
type Changelog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	EntityType string    `gorm:"size:64;not null;index:idx_changelog_entity"`
	EntityID   int64     `gorm:"not null;index:idx_changelog_entity"`
	Action     string    `gorm:"size:32;not null"` // create, update, delete, status_change
	FieldName  string    `gorm:"size:128"`         // empty for create/delete
	OldValue   string    `gorm:"type:text"`        // JSON-encoded, empty for creates
	NewValue   string    `gorm:"type:text"`        // JSON-encoded, empty for deletes
	ChangedBy  int64     `gorm:"not null"`         // user_id from JWT, 0 for system
	ChangedAt  time.Time `gorm:"not null;default:now()"`
	Reason     string    `gorm:"type:text"`
}

func (Changelog) TableName() string {
	return "changelogs"
}
