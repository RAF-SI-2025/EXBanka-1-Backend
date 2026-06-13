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

	// RoutingNumber is the bank that owns this row; stamped to OwnRouting on
	// local create by BeforeCreate. (routing_number, native_id) is the
	// bank-scoped natural key. It is ALSO the first column of ux_otcneg_chain
	// so the "one chain per bidder per parent" invariant is routing-scoped —
	// remote rows (routing = peer) never collide with local ones.
	RoutingNumber int64   `gorm:"not null;default:0;uniqueIndex:ux_otcneg_native,priority:1;uniqueIndex:ux_otcneg_chain,priority:1" json:"routing_number"`
	NativeID      *string `gorm:"size:128;uniqueIndex:ux_otcneg_native,priority:2" json:"native_id,omitempty"`

	// Local is THE authoritative local-vs-remote discriminator: true ⇔ this bank
	// hosts the row (RoutingNumber == OwnRouting()), false ⇔ a remote mirror of a
	// peer's row. Stamped once in BeforeCreate (after routing is finalized) and
	// never mutated. When true, the remote-only columns (NativeID, Remote*,
	// LastActionAt-side peer fields) are NULL; when false, the local-only columns
	// (e.g. BidderAccountID) are unused. Queries discriminate on this column.
	Local bool `gorm:"not null;default:false;index" json:"local"`

	// ParentOfferID points at the OTCOffer listing this chain negotiates
	// against. Foreign-key not modeled with GORM tags (manual integrity)
	// — same convention used by OTCOfferRevision.
	ParentOfferID uint64 `gorm:"not null;index:idx_otcneg_parent;uniqueIndex:ux_otcneg_chain,priority:2" json:"parent_offer_id"`

	BidderOwnerType OwnerType `gorm:"size:8;not null;uniqueIndex:ux_otcneg_chain,priority:3;check:bidder_owner_type IN ('client','bank')" json:"bidder_owner_type"`
	BidderOwnerID   *uint64   `gorm:"uniqueIndex:ux_otcneg_chain,priority:4" json:"bidder_owner_id,omitempty"`
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

	// ActingEmployeeID is the employee who ORIGINATED this bank-owned resource.
	// Non-nil ONLY when the owner is the bank and an employee created it. It is the
	// STABLE SI-TX wire-identity source: the bank party publishes as
	// "employee-<ActingEmployeeID>" on every wire action, regardless of which
	// employee performs later actions. nil for client-owned resources and legacy/seed bank rows.
	ActingEmployeeID *uint64 `gorm:"index" json:"acting_employee_id,omitempty"`

	// MintedContractID is set when the negotiation reached `accepted`
	// status and a contract was successfully minted. Nil for any
	// non-accepted terminal status and for accepted negotiations whose
	// contract mint failed (rare; see AcceptNegotiationResult.Contract
	// == nil case). Populated by OTCNegotiationService.AcceptNegotiation
	// after the formation saga succeeds.
	MintedContractID *uint64 `gorm:"index" json:"minted_contract_id,omitempty"`

	// Remote-mirror columns (SP-2a). Populated ONLY on REMOTE rows
	// (routing_number != OwnRouting()), folded in from the retired
	// peer_otc_negotiation mirror. NULL/zero on local rows. The cross-bank
	// negotiation lifecycle (CreateNegotiation / counter / accept / cascade-
	// cancel webhooks) reads & writes ONLY these columns + Status; the local
	// money paths are routing-guarded (Task 3) so they never observe them.
	//
	//   RemoteOfferJSON      — serialised contract/sitx.OtcOffer (authoritative
	//                          terms for a remote chain; the Quantity/StrikePrice/
	//                          Premium/SettlementDate columns are best-effort
	//                          parses kept only to satisfy the NOT-NULL schema).
	//   RemoteBuyerRouting / RemoteBuyerID   — SI-TX wire buyer party.
	//   RemoteSellerRouting / RemoteSellerID — SI-TX wire seller party.
	//   RemoteParentRouting / RemoteParentNativeID — Phase-10 cascade-cancel
	//                          grouping key (the discovered listing's lot id).
	//
	// The shared Status column carries the PEER status vocabulary on remote
	// rows ("ongoing" | "accepted" | "cancelled" | ...). The SP-1 read shaping
	// understands it; local code never branches on those values because the
	// routing guard keeps remote rows out of every local query.
	RemoteOfferJSON      *string `gorm:"type:text" json:"-"`
	RemoteBuyerRouting   *int64  `json:"-"`
	RemoteBuyerID        *string `gorm:"size:128" json:"-"`
	RemoteSellerRouting  *int64  `json:"-"`
	RemoteSellerID       *string `gorm:"size:128" json:"-"`
	RemoteParentRouting  *int64  `gorm:"index:idx_otcneg_remote_parent,priority:1" json:"-"`
	RemoteParentNativeID *string `gorm:"size:128;index:idx_otcneg_remote_parent,priority:2" json:"-"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"-"`
}

func (OTCNegotiation) TableName() string { return "otc_negotiations" }

// BeforeCreate stamps the own routing number on local rows. Remote rows
// (added in later tasks) arrive with RoutingNumber already set to the peer's
// routing and are left untouched. Tolerates a nil tx (only touches the struct).
func (n *OTCNegotiation) BeforeCreate(tx *gorm.DB) error {
	if n.RoutingNumber == 0 {
		n.RoutingNumber = OwnRouting()
	}
	// Stamp the discriminator AFTER routing is finalized. Must NOT live in
	// BeforeSave: GORM runs BeforeSave BEFORE BeforeCreate, where routing would
	// still be 0 and a local row would be mis-stamped false.
	n.Local = n.RoutingNumber == OwnRouting()
	return nil
}

func (n *OTCNegotiation) BeforeSave(tx *gorm.DB) error {
	if err := ValidateOwner(n.BidderOwnerType, n.BidderOwnerID); err != nil {
		return err
	}
	return ValidateActingEmployee(n.BidderOwnerType, n.ActingEmployeeID)
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
