package model

import "time"

type CardBlock struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CardID    uint64     `gorm:"index;not null" json:"card_id"`
	Reason    string     `gorm:"size:255" json:"reason"`
	BlockedAt time.Time  `gorm:"not null" json:"blocked_at"`
	ExpiresAt *time.Time `json:"expires_at"` // nil = permanent block
	Active    bool       `gorm:"default:true" json:"active"`
	CreatedAt time.Time  `json:"created_at"`
}
