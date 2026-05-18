package model

import "time"

// NotificationTemplate is an admin-customized override of a registry template's
// subject/body. A row exists only when an admin has customized that
// (type, channel); absence means "use the code-defined registry default".
type NotificationTemplate struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Type      string    `gorm:"size:64;not null;uniqueIndex:ux_tmpl_type_channel,priority:1" json:"type"`
	Channel   string    `gorm:"size:16;not null;uniqueIndex:ux_tmpl_type_channel,priority:2" json:"channel"`
	Subject   string    `gorm:"type:text;not null" json:"subject"`
	Body      string    `gorm:"type:text;not null" json:"body"`
	UpdatedBy uint64    `gorm:"not null" json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
