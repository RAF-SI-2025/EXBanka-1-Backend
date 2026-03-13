package model

import "time"

type AuthorizedPerson struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName   string    `gorm:"not null" json:"first_name"`
	LastName    string    `gorm:"not null" json:"last_name"`
	DateOfBirth int64     `gorm:"not null" json:"date_of_birth"`
	Gender      string    `gorm:"size:10" json:"gender"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone"`
	Address     string    `json:"address"`
	AccountID   uint64    `gorm:"not null" json:"account_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
