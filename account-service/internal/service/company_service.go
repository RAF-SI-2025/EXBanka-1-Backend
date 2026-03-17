package service

import (
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
	return s.repo.Create(company)
}

func (s *CompanyService) Get(id uint64) (*model.Company, error) {
	return s.repo.GetByID(id)
}

func (s *CompanyService) Update(company *model.Company) error {
	return s.repo.Update(company)
}
