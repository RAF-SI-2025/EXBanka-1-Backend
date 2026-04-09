package model

import (
	"errors"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ExchangeRate stores a directional currency-pair rate.
// Both directions are stored: e.g. EUR/RSD and RSD/EUR.
type ExchangeRate struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement"`
	FromCurrency string          `gorm:"size:3;not null;uniqueIndex:idx_currency_pair"`
	ToCurrency   string          `gorm:"size:3;not null;uniqueIndex:idx_currency_pair"`
	BuyRate      decimal.Decimal `gorm:"type:numeric(18,8);not null"`
	SellRate     decimal.Decimal `gorm:"type:numeric(18,8);not null"`
	Version      int64           `gorm:"not null;default:1"`
	UpdatedAt    time.Time
}

// BeforeUpdate enforces optimistic locking by adding a WHERE version = ?
// clause and incrementing the version. Callers must check RowsAffected == 0
// after Save to detect conflicts.
func (r *ExchangeRate) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", r.Version)
	r.Version++
	return nil
}

// SeedDefaultRates inserts conservative fallback rates for all supported
// currencies if they are not yet in the database. Called at startup before
// the first API sync so the service is never left with an empty rate table.
func SeedDefaultRates(repo interface {
	GetByPair(from, to string) (*ExchangeRate, error)
	Upsert(from, to string, buy, sell decimal.Decimal) error
}) {
	defaults := []struct {
		from, to  string
		buy, sell float64
	}{
		{"EUR", "RSD", 116.00, 118.00},
		{"RSD", "EUR", 0.00844, 0.00862},
		{"USD", "RSD", 106.00, 108.00},
		{"RSD", "USD", 0.00920, 0.00940},
		{"CHF", "RSD", 119.00, 121.00},
		{"RSD", "CHF", 0.00823, 0.00840},
		{"GBP", "RSD", 134.00, 137.00},
		{"RSD", "GBP", 0.00727, 0.00743},
		{"JPY", "RSD", 0.70, 0.72},
		{"RSD", "JPY", 1.38, 1.42},
		{"CAD", "RSD", 78.00, 80.00},
		{"RSD", "CAD", 0.01245, 0.01282},
		{"AUD", "RSD", 68.00, 70.00},
		{"RSD", "AUD", 0.01420, 0.01470},
	}
	for _, d := range defaults {
		_, err := repo.GetByPair(d.from, d.to)
		if err == nil {
			continue // already exists
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("exchange seed: skipping %s/%s — DB error: %v", d.from, d.to, err)
			continue
		}
		// Record does not exist — seed it.
		_ = repo.Upsert(d.from, d.to,
			decimal.NewFromFloat(d.buy),
			decimal.NewFromFloat(d.sell),
		)
	}
}
