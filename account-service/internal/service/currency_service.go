package service

import (
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

type CurrencyService struct {
	repo *repository.CurrencyRepository
}

func NewCurrencyService(repo *repository.CurrencyRepository) *CurrencyService {
	return &CurrencyService{repo: repo}
}

func (s *CurrencyService) List() ([]model.Currency, error) {
	return s.repo.List()
}

func (s *CurrencyService) GetByCode(code string) (*model.Currency, error) {
	return s.repo.GetByCode(code)
}
