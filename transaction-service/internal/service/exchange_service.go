package service

import (
	"context"
	"log"

	"github.com/shopspring/decimal"

	"github.com/exbanka/transaction-service/internal/model"
	nbs "github.com/exbanka/transaction-service/internal/nbs"
	"github.com/exbanka/transaction-service/internal/repository"
)

type ExchangeService struct {
	exchangeRepo *repository.ExchangeRateRepository
}

func NewExchangeService(exchangeRepo *repository.ExchangeRateRepository) *ExchangeService {
	return &ExchangeService{exchangeRepo: exchangeRepo}
}

// ConvertAmount multiplies amount by rate.
func ConvertAmount(amount, rate decimal.Decimal) decimal.Decimal {
	return amount.Mul(rate)
}

func (s *ExchangeService) GetExchangeRate(fromCurrency, toCurrency string) (*model.ExchangeRate, error) {
	return s.exchangeRepo.GetByPair(fromCurrency, toCurrency)
}

func (s *ExchangeService) ListExchangeRates() ([]model.ExchangeRate, error) {
	return s.exchangeRepo.List()
}

// SyncFromNBS fetches the latest rates from NBS and upserts them into DB.
// On failure, logs a warning and returns the error. Existing DB rates remain.
func (s *ExchangeService) SyncFromNBS(ctx context.Context, provider nbs.RateProvider) error {
	rates, err := provider.FetchRates()
	if err != nil {
		log.Printf("WARN: NBS sync failed, keeping cached rates: %v", err)
		return err
	}
	for code, pair := range rates {
		if err := s.exchangeRepo.Upsert(code, "RSD", pair[0], pair[1]); err != nil {
			log.Printf("WARN: failed to upsert %s/RSD rate: %v", code, err)
		}
		// Store inverse rate (RSD → foreign currency) using sell rate
		if !pair[1].IsZero() {
			inverse := decimal.NewFromInt(1).Div(pair[1])
			if err := s.exchangeRepo.Upsert("RSD", code, inverse, inverse); err != nil {
				log.Printf("WARN: failed to upsert RSD/%s rate: %v", code, err)
			}
		}
	}
	return nil
}
