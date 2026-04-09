package service

import (
	"fmt"
	"time"
)

// SecurityLookupAdapter implements SecurityLookupRepo by composing existing repos.
type SecurityLookupAdapter struct {
	futuresRepo FuturesRepo
}

func NewSecurityLookupAdapter(futuresRepo FuturesRepo) *SecurityLookupAdapter {
	return &SecurityLookupAdapter{futuresRepo: futuresRepo}
}

func (a *SecurityLookupAdapter) GetFuturesSettlementDate(securityID uint64) (time.Time, error) {
	f, err := a.futuresRepo.GetByID(securityID)
	if err != nil {
		return time.Time{}, fmt.Errorf("futures %d not found: %w", securityID, err)
	}
	return f.SettlementDate, nil
}
