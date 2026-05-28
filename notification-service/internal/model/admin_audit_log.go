package model

import "time"

// AdminAuditLog persists admin cron control actions (trigger, pause, resume)
// published by the api-gateway to the admin.cron-action Kafka topic.
// The notification-service is the authoritative audit store because it already
// holds all persistent notification data and has a PostgreSQL database.
type AdminAuditLog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	Action     string    `gorm:"size:32;not null;index"`
	Service    string    `gorm:"size:64;not null;index"`
	CronName   string    `gorm:"size:100;not null;index"`
	EmployeeID int64     `gorm:"not null;index"`
	Reason     string    `gorm:"size:512"`
	Timestamp  time.Time `gorm:"not null;index"`
}

func (AdminAuditLog) TableName() string { return "admin_audit_logs" }
