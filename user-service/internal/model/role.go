package model

import "time"

type Role struct {
	ID          int64        `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string       `gorm:"uniqueIndex;size:50;not null" json:"name"`
	Description string       `gorm:"size:255" json:"description"`
	Permissions []Permission `gorm:"many2many:role_permissions;" json:"permissions"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}
