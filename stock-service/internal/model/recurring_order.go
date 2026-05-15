package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// RecurrenceInterval controls when a recurring order materialises a new
// Market order. Weekly fires once per ISO week on DayOfWeek; monthly
// fires once per month on DayOfMonth (clamped to 28 to avoid Feb edges).
type RecurrenceInterval string

const (
	RecurrenceWeekly  RecurrenceInterval = "weekly"
	RecurrenceMonthly RecurrenceInterval = "monthly"
)

const (
	RecurringOrderStatusActive    = "active"
	RecurringOrderStatusPaused    = "paused"
	RecurringOrderStatusCancelled = "cancelled"
	RecurringOrderStatusFinished  = "finished"
)

// RecurringOrder is the template a scheduler materialises into concrete
// Market orders on each tick. Only the template is editable; materialised
// orders are independent Order rows with their own status.
type RecurringOrder struct {
	ID          uint64             `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerType   OwnerType          `gorm:"type:varchar(16);not null;index:idx_recur_owner,priority:1" json:"owner_type"`
	OwnerID     *uint64            `gorm:"index:idx_recur_owner,priority:2" json:"owner_id,omitempty"`
	ListingID   uint64             `gorm:"not null;index" json:"listing_id"`
	Side        string             `gorm:"type:varchar(8);not null" json:"side"` // buy | sell
	Quantity    int64              `gorm:"not null" json:"quantity"`
	AccountID   uint64             `gorm:"not null" json:"account_id"`
	Interval    RecurrenceInterval `gorm:"type:varchar(16);not null" json:"interval"`
	DayOfMonth  *int               `json:"day_of_month,omitempty"`
	DayOfWeek   *int               `json:"day_of_week,omitempty"`
	StartDate   time.Time          `gorm:"not null" json:"start_date"`
	EndDate     *time.Time         `json:"end_date,omitempty"`
	Status      string             `gorm:"type:varchar(16);not null;default:'active';index" json:"status"`
	LastRun     *time.Time         `json:"last_run,omitempty"`
	NextRun     time.Time          `gorm:"not null;index" json:"next_run"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Version     int64              `gorm:"not null;default:0" json:"-"`
}

func (r *RecurringOrder) BeforeSave(*gorm.DB) error {
	if r.Side != "buy" && r.Side != "sell" {
		return errors.New("side must be buy or sell")
	}
	if r.Quantity <= 0 {
		return errors.New("quantity must be positive")
	}
	switch r.Interval {
	case RecurrenceWeekly:
		if r.DayOfWeek == nil || *r.DayOfWeek < 0 || *r.DayOfWeek > 6 {
			return errors.New("weekly recurring orders require day_of_week 0..6")
		}
	case RecurrenceMonthly:
		if r.DayOfMonth == nil || *r.DayOfMonth < 1 || *r.DayOfMonth > 28 {
			return errors.New("monthly recurring orders require day_of_month 1..28")
		}
	default:
		return errors.New("interval must be weekly or monthly")
	}
	return ValidateOwner(r.OwnerType, r.OwnerID)
}

func (r *RecurringOrder) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", r.Version)
	}
	r.Version++
	return nil
}

// AdvanceNextRun computes the next run time after the supplied "from"
// time. Documented separately so callers (RunDue + tests) share one
// implementation. Day-of-month is clamped to 28 already at validation.
func (r *RecurringOrder) AdvanceNextRun(from time.Time) time.Time {
	from = from.UTC()
	switch r.Interval {
	case RecurrenceWeekly:
		target := *r.DayOfWeek
		// Go's time.Weekday() is 0=Sunday..6=Saturday — matches the
		// proto convention.
		curr := int(from.Weekday())
		delta := (target - curr + 7) % 7
		if delta == 0 {
			delta = 7
		}
		return from.AddDate(0, 0, delta)
	case RecurrenceMonthly:
		target := *r.DayOfMonth
		next := time.Date(from.Year(), from.Month()+1, target, 0, 0, 0, 0, time.UTC)
		return next
	}
	return from.AddDate(0, 0, 1) // unreachable per validation
}
