package model

import "time"

type Client struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName    string    `gorm:"not null" json:"first_name"`
	LastName     string    `gorm:"not null" json:"last_name"`
	DateOfBirth  int64     `gorm:"not null" json:"date_of_birth"`
	Gender       string    `gorm:"size:10" json:"gender"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	Phone        string    `json:"phone"`
	Address      string    `json:"address"`
	JMBG         string    `gorm:"uniqueIndex;size:13" json:"jmbg"`
	PasswordHash string    `gorm:"not null;default:''" json:"-"`
	Salt         string    `gorm:"not null;default:''" json:"-"`
	Active       bool      `gorm:"default:true" json:"active"`
	Activated    bool      `gorm:"default:false" json:"activated"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
