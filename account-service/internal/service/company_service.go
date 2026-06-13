package service

import (
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

type CompanyService struct {
	repo *repository.CompanyRepository
}

func NewCompanyService(repo *repository.CompanyRepository) *CompanyService {
	return &CompanyService{repo: repo}
}

func (s *CompanyService) Create(company *model.Company) error {
	if err := s.repo.Create(company); err != nil {
		// Duplicate registration / tax number → 409 with a clean message (the raw
		// DB error would echo the colliding registration/tax number to the wire).
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrCompanyDuplicate
		}
		return err
	}
	return nil
}

func (s *CompanyService) Get(id uint64) (*model.Company, error) {
	return s.repo.GetByID(id)
}

func (s *CompanyService) GetByOwnerID(ownerID uint64) (*model.Company, error) {
	return s.repo.GetByOwnerID(ownerID)
}

func (s *CompanyService) Update(company *model.Company) error {
	return s.repo.Update(company)
}
