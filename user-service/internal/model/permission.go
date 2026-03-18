package model

import "time"

type Permission struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Code        string    `gorm:"uniqueIndex;size:100;not null" json:"code"`
	Description string    `gorm:"size:255" json:"description"`
	Category    string    `gorm:"size:50;index" json:"category"`
	CreatedAt   time.Time `json:"created_at"`
}
