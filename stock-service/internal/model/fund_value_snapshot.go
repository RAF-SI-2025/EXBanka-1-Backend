package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// FundValueSnapshot is a daily point-in-time record of a fund's NAV and its
// components, written by the fund-snapshot cron. The (fund_id, date) series
// feeds the discovery/detail metrics (annualized return, volatility,
// reward-to-variability, max drawdown) and the historical value chart.
// Mirrors ListingDailyPriceInfo.
type FundValueSnapshot struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID           uint64          `gorm:"not null;uniqueIndex:idx_fund_snapshot_fund_date,priority:1" json:"fund_id"`
	Date             time.Time       `gorm:"type:timestamp;not null;uniqueIndex:idx_fund_snapshot_fund_date,priority:2" json:"date"`
	TotalValueRSD    decimal.Decimal `gorm:"type:numeric(20,4);not null" json:"total_value_rsd"`
	LiquidRSDBal     decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"liquid_rsd_bal"`
	HoldingsValueRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"holdings_value_rsd"`
	InvestorCount    int64           `gorm:"not null;default:0" json:"investor_count"`
}

func (FundValueSnapshot) TableName() string { return "fund_value_snapshots" }
