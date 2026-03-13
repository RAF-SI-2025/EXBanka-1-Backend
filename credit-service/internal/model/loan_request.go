package model

import "time"

type LoanRequest struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID         uint64    `gorm:"not null;index" json:"client_id"`
	LoanType         string    `gorm:"size:30;not null" json:"loan_type"`
	InterestType     string    `gorm:"size:20;not null" json:"interest_type"`
	Amount           float64   `gorm:"not null" json:"amount"`
	CurrencyCode     string    `gorm:"size:3;not null" json:"currency_code"`
	Purpose          string    `json:"purpose"`
	MonthlySalary    float64   `json:"monthly_salary"`
	EmploymentStatus string    `gorm:"size:30" json:"employment_status"`
	EmploymentPeriod int       `json:"employment_period"`
	RepaymentPeriod  int       `gorm:"not null" json:"repayment_period"`
	Phone            string    `json:"phone"`
	AccountNumber    string    `gorm:"not null" json:"account_number"`
	Status           string    `gorm:"size:20;default:'pending'" json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
