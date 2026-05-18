package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// BeforeUpdate adds a WHERE version=? clause and increments the version,
// providing optimistic locking on every Save/Update of an Account.
//
// Fix R9 (2026-05-16): CurrencyCode is also IMMUTABLE post-creation —
// every saga that takes a cross-currency action (OTC accept, OTC
// exercise) captures the account's currency once pre-saga and uses
// the captured value in its Forward closures. If a future code path
// ever tried to UPDATE currency_code mid-flight, those captured values
// would be stale and the reservation/settle would land in the wrong
// currency. The hook below forbids it at the DB write boundary so the
// invariant is enforced regardless of which caller tries it.
func (a *Account) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", a.Version)
	a.Version++
	if tx.Statement != nil && tx.Statement.Changed("CurrencyCode") {
		return errAccountCurrencyImmutable
	}
	return nil
}

// errAccountCurrencyImmutable is returned by the BeforeUpdate hook when
// a caller attempts to change an Account's CurrencyCode. See Fix R9.
var errAccountCurrencyImmutable = errAccountCurrencyImmutableT{}

type errAccountCurrencyImmutableT struct{}

func (errAccountCurrencyImmutableT) Error() string {
	return "account currency_code is immutable post-creation (Fix R9 invariant)"
}

type Account struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement"`
	AccountNumber    string          `gorm:"uniqueIndex;size:18;not null"`
	AccountName      string          `gorm:"size:255"`
	OwnerID          uint64          `gorm:"not null;index:idx_account_owner"`
	OwnerName        string          `gorm:"size:255"`
	Balance          decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	AvailableBalance decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	// ReservedBalance is the running total of active reservations on this
	// account. Maintained by the reservation service inside the same DB
	// transaction as each reserve/release/partial-settle. Never negative.
	// AvailableBalance = Balance - ReservedBalance (computed, never stored).
	ReservedBalance decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0" json:"reserved_balance"`
	EmployeeID      uint64          `gorm:"not null;index"`
	ExpiresAt       time.Time       `gorm:"not null"`
	CurrencyCode    string          `gorm:"size:3;not null;index:idx_account_currency"`
	Status          string          `gorm:"size:20;not null;default:'active';index:idx_account_status"`
	AccountKind     string          `gorm:"size:20;not null"`
	AccountType     string          `gorm:"size:20;not null;default:'standard'"`
	AccountCategory string          `gorm:"size:50"`
	MaintenanceFee  decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	DailyLimit      decimal.Decimal `gorm:"type:numeric(18,4);not null;default:1000000"`
	MonthlyLimit    decimal.Decimal `gorm:"type:numeric(18,4);not null;default:10000000"`
	DailySpending   decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	MonthlySpending decimal.Decimal `gorm:"type:numeric(18,4);not null;default:0"`
	CompanyID       *uint64         `gorm:"index"`
	IsBankAccount   bool            `gorm:"default:false;index" json:"is_bank_account"`
	Version         int64           `gorm:"not null;default:1"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       gorm.DeletedAt `gorm:"index"`
}
