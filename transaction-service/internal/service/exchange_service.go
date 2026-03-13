package service

import (
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

type ExchangeService struct {
	exchangeRepo *repository.ExchangeRateRepository
}

func NewExchangeService(exchangeRepo *repository.ExchangeRateRepository) *ExchangeService {
	return &ExchangeService{exchangeRepo: exchangeRepo}
}

// ConvertAmount multiplies amount by rate.
func ConvertAmount(amount, rate float64) float64 {
	return amount * rate
}

func (s *ExchangeService) GetExchangeRate(fromCurrency, toCurrency string) (*model.ExchangeRate, error) {
	return s.exchangeRepo.GetByPair(fromCurrency, toCurrency)
}

func (s *ExchangeService) ListExchangeRates() ([]model.ExchangeRate, error) {
	return s.exchangeRepo.List()
}
