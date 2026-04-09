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
			return nil, errors.New("stock not found")
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
			return nil, errors.New("futures contract not found")
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
			return nil, errors.New("forex pair not found")
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
			return nil, errors.New("option not found")
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

// GenerateOptionsForStock creates option contracts for a stock using the
// algorithmic approach (Approach 2 from spec):
// 1. Generate settlement dates: every 6 days for 30 days, then 6 more at 30-day intervals
// 2. Strike prices: 5 above and 5 below rounded stock price
// 3. For each date x strike: one CALL + one PUT
func GenerateOptionsForStock(stock *model.Stock) []model.Option {
	if stock.Price.IsZero() {
		return nil
	}

	now := time.Now()
	dates := generateSettlementDates(now)
	strikes := generateStrikePrices(stock.Price)

	var options []model.Option
	for _, date := range dates {
		for _, strike := range strikes {
			dateStr := date.Format("060102")
			strikeCents := strike.Mul(decimal.NewFromInt(100)).IntPart()

			// CALL option
			callTicker := fmt.Sprintf("%s%sC%08d", stock.Ticker, dateStr, strikeCents)
			options = append(options, model.Option{
				Ticker:            callTicker,
				Name:              fmt.Sprintf("%s Call %s $%s", stock.Name, date.Format("Jan 2006"), strike.StringFixed(0)),
				StockID:           stock.ID,
				OptionType:        "call",
				StrikePrice:       strike,
				ImpliedVolatility: decimal.NewFromInt(1),
				Premium:           estimatePremium(stock.Price, strike, date, "call"),
				OpenInterest:      0,
				SettlementDate:    date,
			})

			// PUT option
			putTicker := fmt.Sprintf("%s%sP%08d", stock.Ticker, dateStr, strikeCents)
			options = append(options, model.Option{
				Ticker:            putTicker,
				Name:              fmt.Sprintf("%s Put %s $%s", stock.Name, date.Format("Jan 2006"), strike.StringFixed(0)),
				StockID:           stock.ID,
				OptionType:        "put",
				StrikePrice:       strike,
				ImpliedVolatility: decimal.NewFromInt(1),
				Premium:           estimatePremium(stock.Price, strike, date, "put"),
				OpenInterest:      0,
				SettlementDate:    date,
			})
		}
	}
	return options
}

func generateSettlementDates(now time.Time) []time.Time {
	var dates []time.Time
	// Phase 1: every 6 days until 30 days out
	d := now.AddDate(0, 0, 6)
	for d.Sub(now).Hours()/24 <= 30 {
		dates = append(dates, d)
		d = d.AddDate(0, 0, 6)
	}
	// Phase 2: 6 more dates at 30-day intervals
	last := dates[len(dates)-1]
	for i := 0; i < 6; i++ {
		last = last.AddDate(0, 0, 30)
		dates = append(dates, last)
	}
	return dates
}

func generateStrikePrices(currentPrice decimal.Decimal) []decimal.Decimal {
	rounded := currentPrice.Round(0)
	var strikes []decimal.Decimal
	for i := -5; i <= 5; i++ {
		strikes = append(strikes, rounded.Add(decimal.NewFromInt(int64(i))))
	}
	return strikes
}

// estimatePremium is a simplified premium estimation.
// A proper Black-Scholes implementation could replace this.
func estimatePremium(stockPrice, strikePrice decimal.Decimal, settlementDate time.Time, optionType string) decimal.Decimal {
	daysToExpiry := decimal.NewFromFloat(time.Until(settlementDate).Hours() / 24)
	if daysToExpiry.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}

	// Intrinsic value
	var intrinsic decimal.Decimal
	if optionType == "call" {
		intrinsic = stockPrice.Sub(strikePrice)
	} else {
		intrinsic = strikePrice.Sub(stockPrice)
	}
	if intrinsic.IsNegative() {
		intrinsic = decimal.Zero
	}

	// Time value: rough approximation = stockPrice * 0.01 * sqrt(days/365)
	timeRatio := daysToExpiry.Div(decimal.NewFromInt(365))
	// Approximate sqrt via Babylonian method for one iteration
	sqrtApprox := timeRatio.Add(decimal.NewFromInt(1)).Div(decimal.NewFromInt(2))
	timeValue := stockPrice.Mul(decimal.NewFromFloat(0.01)).Mul(sqrtApprox)

	return intrinsic.Add(timeValue).Round(2)
}
