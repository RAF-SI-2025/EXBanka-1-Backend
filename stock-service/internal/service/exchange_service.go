package service

import (
	"errors"
	"log"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
)

// ExchangeSeedRepo is the repository surface required only for seeding exchanges
// from CSV. It extends the read-only ExchangeRepo with the upsert method.
type ExchangeSeedRepo interface {
	ExchangeRepo
	UpsertByMICCode(exchange *model.StockExchange) error
}

type ExchangeService struct {
	exchangeRepo ExchangeSeedRepo
	settingRepo  SettingRepo
}

func NewExchangeService(
	exchangeRepo ExchangeSeedRepo,
	settingRepo SettingRepo,
) *ExchangeService {
	return &ExchangeService{
		exchangeRepo: exchangeRepo,
		settingRepo:  settingRepo,
	}
}

// SeedExchanges loads exchange data from CSV and upserts into the database.
func (s *ExchangeService) SeedExchanges(csvPath string) error {
	exchanges, err := provider.LoadExchangesFromCSVFile(csvPath)
	if err != nil {
		return err
	}
	for _, ex := range exchanges {
		ex := ex
		if err := s.exchangeRepo.UpsertByMICCode(&ex); err != nil {
			log.Printf("WARN: failed to upsert exchange %s: %v", ex.MICCode, err)
		}
	}
	log.Printf("seeded %d exchanges from CSV", len(exchanges))
	return nil
}

func (s *ExchangeService) GetExchange(id uint64) (*model.StockExchange, error) {
	ex, err := s.exchangeRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("exchange not found")
		}
		return nil, err
	}
	return ex, nil
}

func (s *ExchangeService) ListExchanges(search string, page, pageSize int) ([]model.StockExchange, int64, error) {
	return s.exchangeRepo.List(search, page, pageSize)
}

// SetTestingMode toggles global testing mode. When enabled, exchange hours are
// ignored (all exchanges treated as open).
func (s *ExchangeService) SetTestingMode(enabled bool) error {
	val := "false"
	if enabled {
		val = "true"
	}
	return s.settingRepo.Set("testing_mode", val)
}

// GetTestingMode returns whether testing mode is currently enabled.
func (s *ExchangeService) GetTestingMode() bool {
	val, err := s.settingRepo.Get("testing_mode")
	if err != nil {
		return false
	}
	return val == "true"
}

// IsExchangeOpen checks if the given exchange is currently open for trading.
// When testing mode is enabled, always returns true.
func (s *ExchangeService) IsExchangeOpen(exchangeID uint64) (bool, error) {
	if s.GetTestingMode() {
		return true, nil
	}
	ex, err := s.exchangeRepo.GetByID(exchangeID)
	if err != nil {
		return false, err
	}
	return isWithinTradingHours(ex), nil
}
