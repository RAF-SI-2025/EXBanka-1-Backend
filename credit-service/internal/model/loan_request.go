package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type LoanRequest struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID         uint64          `gorm:"not null;index" json:"client_id"`
	LoanType         string          `gorm:"size:30;not null" json:"loan_type"`
	InterestType     string          `gorm:"size:20;not null" json:"interest_type"`
	Amount           decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"amount"`
	CurrencyCode     string          `gorm:"size:3;not null" json:"currency_code"`
	Purpose          string          `json:"purpose"`
	MonthlySalary    decimal.Decimal `gorm:"type:numeric(18,4)" json:"monthly_salary"`
	EmploymentStatus string          `gorm:"size:30" json:"employment_status"`
	EmploymentPeriod int             `json:"employment_period"`
	RepaymentPeriod  int             `gorm:"not null" json:"repayment_period"`
	Phone            string          `json:"phone"`
	AccountNumber    string          `gorm:"not null" json:"account_number"`
	Status           string          `gorm:"size:20;default:'pending'" json:"status"`
	Version          int64           `gorm:"not null;default:1"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}
