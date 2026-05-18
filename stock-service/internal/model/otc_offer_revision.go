package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// OTCOfferRevision is one entry in the negotiation history. Append-only;
// never updated. revision_number starts at 1 (CREATE) and increments per
// COUNTER. Final ACCEPT or REJECT writes one last row.
type OTCOfferRevision struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OfferID        uint64          `gorm:"not null;uniqueIndex:ux_otc_rev,priority:1" json:"offer_id"`
	RevisionNumber int             `gorm:"not null;uniqueIndex:ux_otc_rev,priority:2" json:"revision_number"`
	Quantity       decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice    decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium        decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate time.Time       `gorm:"type:date;not null" json:"settlement_date"`
	// ModifiedByPrincipalType / ID is the principal (NOT owner) who created
	// this revision. Audit field only — not used for ownership filtering.
	ModifiedByPrincipalType string    `gorm:"size:10;not null" json:"modified_by_principal_type"`
	ModifiedByPrincipalID   uint64    `gorm:"not null" json:"modified_by_principal_id"`
	ActingEmployeeID        *uint64   `gorm:"index" json:"acting_employee_id,omitempty"`
	Action                  string    `gorm:"size:16;not null" json:"action"`
	CreatedAt               time.Time `json:"created_at"`
}
