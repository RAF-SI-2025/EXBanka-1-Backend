package model

import "time"

// PeerOtcNegotiation is the receiver-side persistence of an OTC
// negotiation initiated by a peer bank via POST /api/v3/negotiations.
// Updated on counter-offers (PUT) and acceptance status changes.
//
// Composite-unique on (peer_bank_code, foreign_id) so the same peer
// can't double-create a negotiation under the same id.
type PeerOtcNegotiation struct {
	ID                  uint64 `gorm:"primaryKey"`
	PeerBankCode        string `gorm:"size:8;not null;uniqueIndex:idx_peer_neg_keys"`
	ForeignID           string `gorm:"size:128;not null;uniqueIndex:idx_peer_neg_keys"`
	BuyerRoutingNumber  int64  `gorm:"not null"`
	BuyerID             string `gorm:"size:128;not null"`
	SellerRoutingNumber int64  `gorm:"not null"`
	SellerID            string `gorm:"size:128;not null"`
	OfferJSON           string `gorm:"type:text;not null"` // serialised contract/sitx.OtcOffer
	Status              string `gorm:"size:32;not null;default:ongoing;index"`

	// Phase 10 — cross-bank cascade-cancel grouping (atomic per-listing
	// key). When a bidder discovers a listing via /public-option-offers,
	// they capture the listing's (routingNumber, id) and pass it through
	// the initiate POST so both banks' negotiation rows carry the SAME
	// precise lot key. On accept, the seller's bank cascade-cancels
	// every OTHER ongoing chain with the matching ParentOfferRouting +
	// ParentOfferID (under the same seller). Rows initiated free-form
	// (no discovery) leave these NULL — no cascade applies, so a
	// seller's two LEGITIMATELY DISTINCT listings on the same ticker
	// and same settlement_date never accidentally cancel each other.
	ParentOfferRouting *int64  `gorm:"index:idx_peer_neg_parent,priority:1"`
	ParentOfferID      *string `gorm:"size:128;index:idx_peer_neg_parent,priority:2"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}
