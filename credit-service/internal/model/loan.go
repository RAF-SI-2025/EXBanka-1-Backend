package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// BeforeUpdate adds a WHERE version=? clause and increments the version,
// providing optimistic locking on every Save/Update of a Loan.
func (l *Loan) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", l.Version)
	l.Version++
	return nil
}

type Loan struct {
	ID                    uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	LoanNumber            string          `gorm:"uniqueIndex;not null" json:"loan_number"`
	LoanType              string          `gorm:"size:30;not null" json:"loan_type"`
	AccountNumber         string          `gorm:"not null;index" json:"account_number"`
	Amount                decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"amount"`
	RepaymentPeriod       int             `gorm:"not null" json:"repayment_period"`
	NominalInterestRate   decimal.Decimal `gorm:"type:numeric(8,4);not null" json:"nominal_interest_rate"`
	EffectiveInterestRate decimal.Decimal `gorm:"type:numeric(8,4);not null" json:"effective_interest_rate"`
	ContractDate          time.Time       `json:"contract_date"`
	MaturityDate          time.Time       `json:"maturity_date"`
	NextInstallmentAmount decimal.Decimal `gorm:"type:numeric(18,4)" json:"next_installment_amount"`
	NextInstallmentDate   time.Time       `json:"next_installment_date"`
	RemainingDebt         decimal.Decimal `gorm:"type:numeric(18,4)" json:"remaining_debt"`
	CurrencyCode          string          `gorm:"size:3;not null" json:"currency_code"`
	Status                string          `gorm:"size:20;default:'approved'" json:"status"`
	InterestType          string          `gorm:"size:20;not null" json:"interest_type"`
	BaseRate              decimal.Decimal `gorm:"type:numeric(10,4)" json:"base_rate"`
	BankMargin            decimal.Decimal `gorm:"type:numeric(10,4)" json:"bank_margin"`
	CurrentRate           decimal.Decimal `gorm:"type:numeric(10,4)" json:"current_rate"`
	ClientID              uint64          `gorm:"not null;index" json:"client_id"`
	Version               int64           `gorm:"not null;default:1"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}
