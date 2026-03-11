package model

import "time"

type Employee struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName    string    `gorm:"not null" json:"first_name"`
	LastName     string    `gorm:"not null" json:"last_name"`
	DateOfBirth  time.Time `gorm:"not null" json:"date_of_birth"`
	Gender       string    `gorm:"size:10" json:"gender"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	Phone        string    `json:"phone"`
	Address      string    `json:"address"`
	Username     string    `gorm:"uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Salt         string    `gorm:"not null" json:"-"`
	Position     string    `json:"position"`
	Department   string    `json:"department"`
	Active       bool      `gorm:"default:true" json:"active"`
	Role         string    `gorm:"not null;default:'EmployeeBasic'" json:"role"`
	Activated    bool      `gorm:"default:false" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
