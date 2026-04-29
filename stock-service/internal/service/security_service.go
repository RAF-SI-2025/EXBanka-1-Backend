package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/cache"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/source"
)

const securityCacheTTL = 2 * time.Minute

type SecurityService struct {
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
	optionRepo   OptionRepo
	exchangeRepo ExchangeRepo
	cache        *cache.RedisCache
}

func NewSecurityService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo ExchangeRepo,
	redisCache *cache.RedisCache,
) *SecurityService {
	return &SecurityService{
		stockRepo:    stockRepo,
		futuresRepo:  futuresRepo,
		forexRepo:    forexRepo,
		optionRepo:   optionRepo,
		exchangeRepo: exchangeRepo,
		cache:        redisCache,
	}
}

// --- Stocks ---

func (s *SecurityService) ListStocks(filter repository.StockFilter) ([]model.Stock, int64, error) {
	return s.stockRepo.List(filter)
}

func (s *SecurityService) GetStock(id uint64) (*model.Stock, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:stock:%d", id)

	if s.cache != nil {
		var cached model.Stock
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	stock, err := s.stockRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("stock not found: %w", ErrStockNotFound)
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, stock, securityCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return stock, nil
}

// GetStockWithOptions returns a stock with its associated options.
func (s *SecurityService) GetStockWithOptions(id uint64) (*model.Stock, []model.Option, error) {
	stock, err := s.GetStock(id)
	if err != nil {
		return nil, nil, err
	}

	stockID := stock.ID
	options, _, err := s.optionRepo.List(repository.OptionFilter{
		StockID:  &stockID,
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		return nil, nil, err
	}
	return stock, options, nil
}

// --- Futures ---

func (s *SecurityService) ListFutures(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
	return s.futuresRepo.List(filter)
}

func (s *SecurityService) GetFutures(id uint64) (*model.FuturesContract, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:futures:%d", id)

	if s.cache != nil {
		var cached model.FuturesContract
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	f, err := s.futuresRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("futures contract not found: %w", ErrFuturesNotFound)
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, f, securityCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return f, nil
}

// --- Forex ---

func (s *SecurityService) ListForexPairs(filter repository.ForexFilter) ([]model.ForexPair, int64, error) {
	return s.forexRepo.List(filter)
}

func (s *SecurityService) GetForexPair(id uint64) (*model.ForexPair, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:forex:%d", id)

	if s.cache != nil {
		var cached model.ForexPair
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	fp, err := s.forexRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("forex pair not found: %w", ErrForexPairNotFound)
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, fp, securityCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return fp, nil
}

// --- Options ---

func (s *SecurityService) ListOptions(filter repository.OptionFilter) ([]model.Option, int64, error) {
	return s.optionRepo.List(filter)
}

func (s *SecurityService) GetOption(id uint64) (*model.Option, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:option:%d", id)

	if s.cache != nil {
		var cached model.Option
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	o, err := s.optionRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("option not found: %w", ErrOptionNotFound)
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, o, securityCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return o, nil
}

// --- Computed Fields Helpers ---

// StockChangePercent = 100 * Change / (Price - Change).
func StockChangePercent(price, change decimal.Decimal) decimal.Decimal {
	base := price.Sub(change)
	if base.IsZero() {
		return decimal.Zero
	}
	return change.Mul(decimal.NewFromInt(100)).Div(base)
}

// GenerateOptionsForStock creates option contracts for a stock.
// Delegates to source.GenerateOptionsForStock — the canonical implementation
// lives there to avoid an import cycle (source → service).
func GenerateOptionsForStock(stock *model.Stock) []model.Option {
	return source.GenerateOptionsForStock(stock)
}
