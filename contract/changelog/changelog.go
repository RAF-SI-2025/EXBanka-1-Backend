// contract/changelog/changelog.go
package changelog

import (
	"encoding/json"
	"fmt"
	"time"
)

// Action constants for changelog entries.
const (
	ActionCreate       = "create"
	ActionUpdate       = "update"
	ActionDelete       = "delete"
	ActionStatusChange = "status_change"
)

// Entry represents a single field-level change to an entity.
// This is the service-agnostic struct -- each service maps it to its own GORM model.
type Entry struct {
	EntityType string
	EntityID   int64
	Action     string
	FieldName  string // empty for create/delete
	OldValue   string // JSON-encoded, empty for creates
	NewValue   string // JSON-encoded, empty for deletes
	ChangedBy  int64  // user_id from JWT, 0 for system
	ChangedAt  time.Time
	Reason     string
}

// FieldChange captures a before/after pair for a single field.
type FieldChange struct {
	Field    string
	OldValue interface{}
	NewValue interface{}
}

// Diff compares old and new values for the given fields and returns entries
// for every field that actually changed. This avoids boilerplate in every service.
func Diff(entityType string, entityID int64, changedBy int64, reason string, changes []FieldChange) []Entry {
	var entries []Entry
	now := time.Now()
	for _, c := range changes {
		oldJSON := toJSON(c.OldValue)
		newJSON := toJSON(c.NewValue)
		if oldJSON == newJSON {
			continue
		}
		entries = append(entries, Entry{
			EntityType: entityType,
			EntityID:   entityID,
			Action:     ActionUpdate,
			FieldName:  c.Field,
			OldValue:   oldJSON,
			NewValue:   newJSON,
			ChangedBy:  changedBy,
			ChangedAt:  now,
			Reason:     reason,
		})
	}
	return entries
}

// NewCreateEntry creates a single changelog entry for an entity creation.
func NewCreateEntry(entityType string, entityID int64, changedBy int64, newValueJSON string) Entry {
	return Entry{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     ActionCreate,
		NewValue:   newValueJSON,
		ChangedBy:  changedBy,
		ChangedAt:  time.Now(),
	}
}

// NewStatusChangeEntry creates a single changelog entry for a status transition.
func NewStatusChangeEntry(entityType string, entityID int64, changedBy int64, oldStatus, newStatus, reason string) Entry {
	return Entry{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     ActionStatusChange,
		FieldName:  "status",
		OldValue:   toJSON(oldStatus),
		NewValue:   toJSON(newStatus),
		ChangedBy:  changedBy,
		ChangedAt:  time.Now(),
		Reason:     reason,
	}
}

// ToJSON is a public wrapper for JSON-encoding a value for changelog storage.
func ToJSON(v interface{}) string {
	return toJSON(v)
}

func toJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	// Fast path for strings -- avoid double-quoting.
	if s, ok := v.(string); ok {
		b, _ := json.Marshal(s)
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
