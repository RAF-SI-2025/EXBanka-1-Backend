package service

import (
	"fmt"
	"time"
)

// SecurityLookupAdapter implements SecurityLookupRepo by composing existing repos.
type SecurityLookupAdapter struct {
	stockRepo   StockRepo
	futuresRepo FuturesRepo
	forexRepo   ForexPairRepo
	optionRepo  OptionRepo
}

func NewSecurityLookupAdapter(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
) *SecurityLookupAdapter {
	return &SecurityLookupAdapter{
		stockRepo:   stockRepo,
		futuresRepo: futuresRepo,
		forexRepo:   forexRepo,
		optionRepo:  optionRepo,
	}
}

func (a *SecurityLookupAdapter) GetFuturesSettlementDate(securityID uint64) (time.Time, error) {
	f, err := a.futuresRepo.GetByID(securityID)
	if err != nil {
		return time.Time{}, fmt.Errorf("futures %d not found: %w", securityID, err)
	}
	return f.SettlementDate, nil
}

// GetSecurityTicker resolves the human-readable ticker of a security. Used by
// the order-placement path to stamp order.Ticker so it carries through to the
// Holding row and /me/portfolio responses. Best-effort: a failed lookup
// returns an empty string (and nil error) so placement is not blocked by a
// missing ticker lookup.
func (a *SecurityLookupAdapter) GetSecurityTicker(securityType string, securityID uint64) (string, error) {
	switch securityType {
	case "stock":
		if a.stockRepo == nil {
			return "", nil
		}
		s, err := a.stockRepo.GetByID(securityID)
		if err != nil {
			return "", nil
		}
		return s.Ticker, nil
	case "futures":
		if a.futuresRepo == nil {
			return "", nil
		}
		f, err := a.futuresRepo.GetByID(securityID)
		if err != nil {
			return "", nil
		}
		return f.Ticker, nil
	case "forex":
		if a.forexRepo == nil {
			return "", nil
		}
		fp, err := a.forexRepo.GetByID(securityID)
		if err != nil {
			return "", nil
		}
		return fp.Ticker, nil
	case "option":
		if a.optionRepo == nil {
			return "", nil
		}
		o, err := a.optionRepo.GetByID(securityID)
		if err != nil {
			return "", nil
		}
		return o.Ticker, nil
	}
	return "", nil
}
