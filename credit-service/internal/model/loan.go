package model

import "time"

type Loan struct {
	ID                    uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	LoanNumber            string    `gorm:"uniqueIndex;not null" json:"loan_number"`
	LoanType              string    `gorm:"size:30;not null" json:"loan_type"`
	AccountNumber         string    `gorm:"not null;index" json:"account_number"`
	Amount                float64   `gorm:"not null" json:"amount"`
	RepaymentPeriod       int       `gorm:"not null" json:"repayment_period"`
	NominalInterestRate   float64   `gorm:"not null" json:"nominal_interest_rate"`
	EffectiveInterestRate float64   `gorm:"not null" json:"effective_interest_rate"`
	ContractDate          time.Time `json:"contract_date"`
	MaturityDate          time.Time `json:"maturity_date"`
	NextInstallmentAmount float64   `json:"next_installment_amount"`
	NextInstallmentDate   time.Time `json:"next_installment_date"`
	RemainingDebt         float64   `json:"remaining_debt"`
	CurrencyCode          string    `gorm:"size:3;not null" json:"currency_code"`
	Status                string    `gorm:"size:20;default:'approved'" json:"status"`
	InterestType          string    `gorm:"size:20;not null" json:"interest_type"`
	ClientID              uint64    `gorm:"not null;index" json:"client_id"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}
