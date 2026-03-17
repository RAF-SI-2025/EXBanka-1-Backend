package model

import "time"

type Company struct {
	ID                 uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CompanyName        string    `gorm:"not null" json:"company_name"`
	RegistrationNumber string    `gorm:"uniqueIndex;size:8;not null" json:"registration_number"`
	TaxNumber          string    `gorm:"uniqueIndex;size:9;not null" json:"tax_number"`
	ActivityCode       string    `gorm:"size:5" json:"activity_code"`
	Address            string    `json:"address"`
	OwnerID            uint64    `gorm:"not null" json:"owner_id"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
