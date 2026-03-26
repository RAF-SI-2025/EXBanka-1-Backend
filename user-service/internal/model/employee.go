package model

import "time"

type Employee struct {
	ID                    int64        `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName             string       `gorm:"not null" json:"first_name"`
	LastName              string       `gorm:"not null" json:"last_name"`
	DateOfBirth           time.Time    `gorm:"not null" json:"date_of_birth"`
	Gender                string       `gorm:"size:10" json:"gender"`
	Email                 string       `gorm:"uniqueIndex;not null" json:"email"`
	Phone                 string       `json:"phone"`
	Address               string       `json:"address"`
	JMBG                  string       `gorm:"uniqueIndex;size:13" json:"jmbg"`
	Username              string       `gorm:"uniqueIndex;not null" json:"username"`
	Position              string       `json:"position"`
	Department            string       `json:"department"`
	Roles                 []Role       `gorm:"many2many:employee_roles;" json:"roles"`
	AdditionalPermissions []Permission `gorm:"many2many:employee_additional_permissions;" json:"additional_permissions,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}
