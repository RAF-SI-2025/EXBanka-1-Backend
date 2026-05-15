package model

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// PriceAlertCondition controls how Threshold is compared against the
// current price (or daily-change-percent).
type PriceAlertCondition string

const (
	PriceAlertConditionGTE               PriceAlertCondition = "gte"
	PriceAlertConditionLTE               PriceAlertCondition = "lte"
	PriceAlertConditionDailyChangePctGTE PriceAlertCondition = "daily_change_pct_gte"
	PriceAlertConditionDailyChangePctLTE PriceAlertCondition = "daily_change_pct_lte"
)

func (c PriceAlertCondition) Valid() bool {
	switch c {
	case PriceAlertConditionGTE, PriceAlertConditionLTE,
		PriceAlertConditionDailyChangePctGTE, PriceAlertConditionDailyChangePctLTE:
		return true
	}
	return false
}

// PriceAlert is a per-owner trigger on a single listing. Evaluation happens
// reactively from the price-refresh path: whenever a listing's latest
// price changes, the evaluator scans active alerts on that listing and
// fires a notification on match.
//
// Single-shot alerts flip Active=false on match. Recurring alerts honour
// Cooldown seconds between consecutive firings on the same row.
type PriceAlert struct {
	ID            uint64              `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType     OwnerType           `gorm:"type:varchar(16);not null;index:idx_alert_owner,priority:1" json:"owner_type"`
	OwnerID       *uint64             `gorm:"index:idx_alert_owner,priority:2" json:"owner_id,omitempty"`
	ListingID     uint64              `gorm:"not null;index" json:"listing_id"`
	Condition     PriceAlertCondition `gorm:"type:varchar(32);not null" json:"condition"`
	Threshold     decimal.Decimal     `gorm:"type:numeric(20,8);not null" json:"threshold"`
	IsRecurring   bool                `gorm:"not null;default:false" json:"is_recurring"`
	Cooldown      int                 `gorm:"not null;default:3600" json:"cooldown_seconds"`
	EmailToo      bool                `gorm:"not null;default:false" json:"email_too"`
	Active        bool                `gorm:"not null;default:true;index" json:"active"`
	LastTriggered *time.Time          `json:"last_triggered,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Version       int64               `gorm:"not null;default:0" json:"-"`
}

func (a *PriceAlert) BeforeSave(*gorm.DB) error {
	if !a.Condition.Valid() {
		return errors.New("invalid condition")
	}
	if a.Threshold.Sign() <= 0 {
		return errors.New("threshold must be positive")
	}
	if a.Cooldown < 60 || a.Cooldown > 86400 {
		return errors.New("cooldown_seconds must be in [60, 86400]")
	}
	return ValidateOwner(a.OwnerType, a.OwnerID)
}

func (a *PriceAlert) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", a.Version)
	}
	a.Version++
	return nil
}
