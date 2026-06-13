package model

import "time"

// BusinessAuditLog persists high-value business actions (limit changes,
// usedLimit resets, order approve/reject, permission changes, manual tax
// collection) published by the api-gateway to the admin.business-action Kafka
// topic. The actor (who) is known from the JWT at the gateway. Mirrors
// AdminAuditLog; notification-service is the authoritative audit store.
type BusinessAuditLog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	Action     string    `gorm:"size:32;not null;index"` // limit.set | limit.used_reset | order.approve | order.decline | permissions.set | tax.collect
	ActorID    int64     `gorm:"not null;index"`         // employee who performed the action
	TargetType string    `gorm:"size:32;not null;index"` // employee | order | role | tax
	TargetID   string    `gorm:"size:64;not null;index"`
	Detail     string    `gorm:"size:512"`
	Timestamp  time.Time `gorm:"not null;index"`
}

func (BusinessAuditLog) TableName() string { return "business_audit_logs" }
