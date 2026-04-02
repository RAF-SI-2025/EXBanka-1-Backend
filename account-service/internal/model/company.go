package model

import (
	"time"

	"gorm.io/gorm"
)

// BeforeUpdate adds a WHERE version=? clause and increments the version,
// providing optimistic locking on every Save/Update of a Company.
func (c *Company) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", c.Version)
	c.Version++
	return nil
}

type Company struct {
	ID                 uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CompanyName        string    `gorm:"not null" json:"company_name"`
	RegistrationNumber string    `gorm:"uniqueIndex;size:8;not null" json:"registration_number"`
	TaxNumber          string    `gorm:"uniqueIndex;size:9;not null" json:"tax_number"`
	ActivityCode       string    `gorm:"size:5" json:"activity_code"`
	Address            string    `json:"address"`
	OwnerID            uint64    `gorm:"not null" json:"owner_id"`
	Version            int64     `gorm:"not null;default:1"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
