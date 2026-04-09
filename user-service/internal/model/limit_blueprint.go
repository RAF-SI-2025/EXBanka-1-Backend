package model

import (
	"time"

	"gorm.io/datatypes"
)

// BlueprintType constants for the Type discriminator.
const (
	BlueprintTypeEmployee = "employee"
	BlueprintTypeActuary  = "actuary"
	BlueprintTypeClient   = "client"
)

// ValidBlueprintTypes is the set of allowed Type values.
var ValidBlueprintTypes = map[string]bool{
	BlueprintTypeEmployee: true,
	BlueprintTypeActuary:  true,
	BlueprintTypeClient:   true,
}

// LimitBlueprint is a named, reusable set of limit values that can be applied
// to employees, actuaries, or clients. The Values column stores type-specific
// JSON (employee limit fields, actuary limit fields, or client limit fields).
type LimitBlueprint struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string         `gorm:"size:100;not null;uniqueIndex:idx_blueprint_name_type" json:"name"`
	Description string         `gorm:"size:512" json:"description"`
	Type        string         `gorm:"size:20;not null;uniqueIndex:idx_blueprint_name_type" json:"type"`
	Values      datatypes.JSON `gorm:"type:jsonb;not null" json:"values"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// EmployeeBlueprintValues is the JSON schema for employee blueprint values.
type EmployeeBlueprintValues struct {
	MaxLoanApprovalAmount string `json:"max_loan_approval_amount"`
	MaxSingleTransaction  string `json:"max_single_transaction"`
	MaxDailyTransaction   string `json:"max_daily_transaction"`
	MaxClientDailyLimit   string `json:"max_client_daily_limit"`
	MaxClientMonthlyLimit string `json:"max_client_monthly_limit"`
}

// ActuaryBlueprintValues is the JSON schema for actuary blueprint values.
type ActuaryBlueprintValues struct {
	Limit        string `json:"limit"`
	NeedApproval bool   `json:"need_approval"`
}

// ClientBlueprintValues is the JSON schema for client blueprint values.
type ClientBlueprintValues struct {
	DailyLimit    string `json:"daily_limit"`
	MonthlyLimit  string `json:"monthly_limit"`
	TransferLimit string `json:"transfer_limit"`
}
