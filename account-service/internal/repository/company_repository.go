package repository

import (
	"github.com/exbanka/account-service/internal/model"
	"gorm.io/gorm"
)

type CompanyRepository struct {
	db *gorm.DB
}

func NewCompanyRepository(db *gorm.DB) *CompanyRepository {
	return &CompanyRepository{db: db}
}

func (r *CompanyRepository) Create(company *model.Company) error {
	return r.db.Create(company).Error
}

func (r *CompanyRepository) GetByID(id uint64) (*model.Company, error) {
	var company model.Company
	if err := r.db.First(&company, id).Error; err != nil {
		return nil, err
	}
	return &company, nil
}

func (r *CompanyRepository) GetByOwnerID(ownerID uint64) (*model.Company, error) {
	var company model.Company
	if err := r.db.Where("owner_id = ?", ownerID).First(&company).Error; err != nil {
		return nil, err
	}
	return &company, nil
}

func (r *CompanyRepository) Update(company *model.Company) error {
	return r.db.Save(company).Error
}
