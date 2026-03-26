package service

import (
	"context"
	"fmt"
	"log"

	"github.com/shopspring/decimal"

	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/provider"
	"github.com/exbanka/exchange-service/internal/repository"
)

// ExchangeService handles rate syncing and currency conversion.
// The bank always uses the SELLING rate. Commission is applied per conversion leg.
type ExchangeService struct {
	repo           *repository.ExchangeRateRepository
	commissionRate decimal.Decimal // e.g. 0.005 for 0.5%
	spread         decimal.Decimal // e.g. 0.003 for 0.3%, applied to derive buy/sell from mid
}

func NewExchangeService(repo *repository.ExchangeRateRepository, commissionRate, spread string) (*ExchangeService, error) {
	cr, err := decimal.NewFromString(commissionRate)
	if err != nil {
		return nil, fmt.Errorf("invalid commission rate %q: %w", commissionRate, err)
	}
	sp, err := decimal.NewFromString(spread)
	if err != nil {
		return nil, fmt.Errorf("invalid spread %q: %w", spread, err)
	}
	return &ExchangeService{repo: repo, commissionRate: cr, spread: sp}, nil
}

// SyncRates fetches mid-market rates from the provider and upserts buy/sell
// pairs (both directions) into the database. If the provider fails, existing
// cached rates are preserved and the error is returned.
func (s *ExchangeService) SyncRates(ctx context.Context, p provider.RateProvider) error {
	rates, err := p.FetchRatesFromRSD()
	if err != nil {
		log.Printf("WARN: rate provider sync failed, keeping cached rates: %v", err)
		return err
	}

	one := decimal.NewFromInt(1)
	var upsertErrors int
	for code, midRsdToC := range rates {
		if midRsdToC.IsZero() {
			continue
		}
		// RSD → foreign direction
		buyRsdToC := midRsdToC.Mul(one.Sub(s.spread))
		sellRsdToC := midRsdToC.Mul(one.Add(s.spread))
		if err := s.repo.Upsert("RSD", code, buyRsdToC, sellRsdToC); err != nil {
			log.Printf("WARN: failed to upsert RSD/%s: %v", code, err)
			upsertErrors++
		}

		// foreign → RSD direction (inverse)
		midCToRsd := one.Div(midRsdToC)
		buyCToRsd := midCToRsd.Mul(one.Sub(s.spread))
		sellCToRsd := midCToRsd.Mul(one.Add(s.spread))
		if err := s.repo.Upsert(code, "RSD", buyCToRsd, sellCToRsd); err != nil {
			log.Printf("WARN: failed to upsert %s/RSD: %v", code, err)
			upsertErrors++
		}
	}
	if upsertErrors > 0 {
		return fmt.Errorf("sync completed with %d upsert failure(s); check logs for details", upsertErrors)
	}
	return nil
}

// Convert performs a raw sell-rate conversion with no commission. Used internally
// by the transaction service to determine the recipient credit amount before fees.
// Returns (convertedAmount, effectiveRate, error).
//
// Conversion paths:
//   - same currency: returns (amount, 1, nil)
//   - RSD → foreign: single leg using repo.GetByPair("RSD", toCurrency)
//   - foreign → RSD: single leg using repo.GetByPair(fromCurrency, "RSD")
//   - foreign → foreign: two legs via RSD intermediary
func (s *ExchangeService) Convert(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	if from == to {
		return amount, decimal.NewFromInt(1), nil
	}
	if from == "RSD" {
		return s.singleLeg("RSD", to, amount)
	}
	if to == "RSD" {
		return s.singleLeg(from, "RSD", amount)
	}
	// Two-step: from → RSD → to
	return s.twoLeg(from, to, amount)
}

// Calculate is the informational endpoint: applies selling rate + commission per leg.
// Returns (netConverted, commissionRate, effectiveRate, error).
// For cross-currency, commission is deducted from the output of each conversion leg.
func (s *ExchangeService) Calculate(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	if from == to {
		return amount, decimal.Zero, decimal.NewFromInt(1), nil
	}
	if from == "RSD" || to == "RSD" {
		gross, effRate, err := s.Convert(ctx, from, to, amount)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, err
		}
		commission := gross.Mul(s.commissionRate)
		return gross.Sub(commission), s.commissionRate, effRate, nil
	}
	// Two-step with per-leg commission
	// Step 1: from → RSD
	rateStep1, err := s.repo.GetByPair(from, "RSD")
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup %s/RSD: %w", from, err)
	}
	grossRSD := amount.Mul(rateStep1.SellRate)
	commRSD := grossRSD.Mul(s.commissionRate)
	netRSD := grossRSD.Sub(commRSD)

	// Step 2: RSD → to
	rateStep2, err := s.repo.GetByPair("RSD", to)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup RSD/%s: %w", to, err)
	}
	grossTarget := netRSD.Mul(rateStep2.SellRate)
	commTarget := grossTarget.Mul(s.commissionRate)
	netTarget := grossTarget.Sub(commTarget)

	effRate := rateStep1.SellRate.Mul(rateStep2.SellRate)
	return netTarget, s.commissionRate, effRate, nil
}

// ListRates returns all stored rates.
func (s *ExchangeService) ListRates() ([]model.ExchangeRate, error) {
	return s.repo.List()
}

// GetRate returns a single rate pair.
func (s *ExchangeService) GetRate(from, to string) (*model.ExchangeRate, error) {
	return s.repo.GetByPair(from, to)
}

// singleLeg converts amount using the sell rate of the given pair.
func (s *ExchangeService) singleLeg(from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	rate, err := s.repo.GetByPair(from, to)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup %s/%s: %w", from, to, err)
	}
	return amount.Mul(rate.SellRate), rate.SellRate, nil
}

// twoLeg implements the cross-currency path: from → RSD → to (no commission).
func (s *ExchangeService) twoLeg(from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	rateFromRSD, err := s.repo.GetByPair(from, "RSD")
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup %s/RSD: %w", from, err)
	}
	rsdAmount := amount.Mul(rateFromRSD.SellRate)

	rateToTarget, err := s.repo.GetByPair("RSD", to)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup RSD/%s: %w", to, err)
	}
	converted := rsdAmount.Mul(rateToTarget.SellRate)
	effRate := rateFromRSD.SellRate.Mul(rateToTarget.SellRate)
	return converted, effRate, nil
}
