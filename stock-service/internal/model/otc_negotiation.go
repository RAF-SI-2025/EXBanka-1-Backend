// Package model — OTCNegotiation is one bidder's negotiation chain
// against a parent OTCOffer (which becomes the immutable LISTING).
//
// Plan: docs/superpowers/plans/2026-05-16-otc-options-marketplace.md §1.
// Many users can each open their own negotiation chain against the same
// parent listing; first chain to ACCEPT wins atomically (parent flips
// to "consumed" and siblings auto-cancel inside a single TX with
// SELECT FOR UPDATE on the parent).
package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	// OTCNegotiation lifecycle. "open" → "countered"* → terminal.
	OTCNegotiationStatusOpen      = "open"      // initial bid placed, awaiting counter/accept/reject
	OTCNegotiationStatusCountered = "countered" // most recent revision was a counter; waiting on other side
	OTCNegotiationStatusAccepted  = "accepted"  // contract minted; parent flips to consumed
	OTCNegotiationStatusRejected  = "rejected"  // explicit reject; chain ends without contract
	OTCNegotiationStatusCancelled = "cancelled" // cancelled by bidder OR cascade-cancelled by sibling accept
	OTCNegotiationStatusExpired   = "expired"   // parent listing expired before resolution

	// OTCNegotiationActionBid     — bidder opens the chain (revision #1 carries the initial bid)
	// OTCNegotiationActionCounter — either side proposes new terms
	// OTCNegotiationActionAccept  — terminal: terms agreed, contract formation begins
	// OTCNegotiationActionReject  — terminal: chain killed without contract
	OTCNegotiationActionBid     = "BID"
	OTCNegotiationActionCounter = "COUNTER"
	OTCNegotiationActionAccept  = "ACCEPT"
	OTCNegotiationActionReject  = "REJECT"
)

// OTCNegotiation is one bidder's chain against a parent OTCOffer listing.
// (parent_offer_id, bidder_owner_type, bidder_owner_id) is the chain key —
// a single bidder cannot open two parallel chains against the same listing.
//
// Note on direction semantics:
//   - parent.direction == "sell_initiated" (poster is the would-be seller) ⇒
//     the bidder is a would-be BUYER who'll pay the premium on accept.
//   - parent.direction == "buy_initiated" (poster wants to buy) ⇒
//     the bidder is a would-be SELLER offering shares; the parent poster
//     pays the premium on accept.
type OTCNegotiation struct {
	ID uint64 `gorm:"primaryKey;autoIncrement" json:"id"`

	// ParentOfferID points at the OTCOffer listing this chain negotiates
	// against. Foreign-key not modeled with GORM tags (manual integrity)
	// — same convention used by OTCOfferRevision.
	ParentOfferID uint64 `gorm:"not null;index:idx_otcneg_parent;uniqueIndex:ux_otcneg_chain,priority:1" json:"parent_offer_id"`

	BidderOwnerType OwnerType `gorm:"size:8;not null;uniqueIndex:ux_otcneg_chain,priority:2;check:bidder_owner_type IN ('client','bank')" json:"bidder_owner_type"`
	BidderOwnerID   *uint64   `gorm:"uniqueIndex:ux_otcneg_chain,priority:3" json:"bidder_owner_id,omitempty"`
	BidderBankCode  *string   `gorm:"size:32" json:"bidder_bank_code,omitempty"`

	// BidderAccountID is the bidder's account bound at chain creation:
	// it pays/receives the premium on accept depending on parent.direction.
	BidderAccountID uint64 `gorm:"not null;default:0" json:"bidder_account_id"`

	// Current terms (snapshot of the latest revision for fast lookup).
	// History lives in OTCNegotiationRevision rows.
	Quantity       decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice    decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium        decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate time.Time       `gorm:"type:date;not null" json:"settlement_date"`

	Status string `gorm:"size:16;not null;index" json:"status"`

	// LastActionBy{Principal,Owner} — who moved last. PrincipalType is
	// "client" or "employee" (audit); OwnerType is the resource owner
	// (the bidder on bid/counter from bidder side; the parent's poster on
	// counter/accept/reject from listing side).
	LastActionByPrincipalType string    `gorm:"size:10;not null" json:"last_action_by_principal_type"`
	LastActionByPrincipalID   uint64    `gorm:"not null" json:"last_action_by_principal_id"`
	LastActionByOwnerType     string    `gorm:"size:8;not null" json:"last_action_by_owner_type"`
	LastActionByOwnerID       *uint64   `json:"last_action_by_owner_id,omitempty"`
	LastActionAt              time.Time `gorm:"not null" json:"last_action_at"`

	ActingEmployeeID *uint64 `gorm:"index" json:"acting_employee_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"-"`
}

func (OTCNegotiation) TableName() string { return "otc_negotiations" }

func (n *OTCNegotiation) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(n.BidderOwnerType, n.BidderOwnerID)
}

func (n *OTCNegotiation) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", n.Version)
	}
	n.Version++
	return nil
}

// IsTerminal reports whether the negotiation chain is in a terminal state
// and cannot be counter-ed, accepted, or rejected.
func (n *OTCNegotiation) IsTerminal() bool {
	switch n.Status {
	case OTCNegotiationStatusAccepted, OTCNegotiationStatusRejected,
		OTCNegotiationStatusCancelled, OTCNegotiationStatusExpired:
		return true
	}
	return false
}
