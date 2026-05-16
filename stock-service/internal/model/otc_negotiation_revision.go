// Package model — OTCNegotiationRevision is one entry in a negotiation
// chain's history. Append-only; never updated. revision_number starts at 1
// (BID) and increments per COUNTER. Final ACCEPT or REJECT writes one last
// row.
//
// Plan: docs/superpowers/plans/2026-05-16-otc-options-marketplace.md §1.
// Parallel to OTCOfferRevision but keyed on negotiation_id rather than
// offer_id, so each bidder's chain has its own independent history.
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// OTCNegotiationRevision is one move in an OTCNegotiation chain.
type OTCNegotiationRevision struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	NegotiationID  uint64 `gorm:"not null;uniqueIndex:ux_otcnegrev,priority:1" json:"negotiation_id"`
	RevisionNumber int    `gorm:"not null;uniqueIndex:ux_otcnegrev,priority:2" json:"revision_number"`

	Quantity       decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice    decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium        decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate time.Time       `gorm:"type:date;not null" json:"settlement_date"`

	// ModifiedByPrincipalType / ID is the principal (NOT owner) who
	// created this revision. Audit field only.
	ModifiedByPrincipalType string  `gorm:"size:10;not null" json:"modified_by_principal_type"`
	ModifiedByPrincipalID   uint64  `gorm:"not null" json:"modified_by_principal_id"`
	ActingEmployeeID        *uint64 `gorm:"index" json:"acting_employee_id,omitempty"`

	// Action is one of OTCNegotiationActionBid/Counter/Accept/Reject.
	Action string `gorm:"size:16;not null" json:"action"`

	CreatedAt time.Time `json:"created_at"`
}

func (OTCNegotiationRevision) TableName() string { return "otc_negotiation_revisions" }
