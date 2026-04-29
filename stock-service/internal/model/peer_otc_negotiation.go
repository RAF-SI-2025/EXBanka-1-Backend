package model

import "time"

// PeerOtcNegotiation is the receiver-side persistence of an OTC
// negotiation initiated by a peer bank via POST /api/v3/negotiations.
// Updated on counter-offers (PUT) and acceptance status changes.
//
// Composite-unique on (peer_bank_code, foreign_id) so the same peer
// can't double-create a negotiation under the same id.
type PeerOtcNegotiation struct {
	ID                  uint64    `gorm:"primaryKey"`
	PeerBankCode        string    `gorm:"size:8;not null;uniqueIndex:idx_peer_neg_keys"`
	ForeignID           string    `gorm:"size:128;not null;uniqueIndex:idx_peer_neg_keys"`
	BuyerRoutingNumber  int64     `gorm:"not null"`
	BuyerID             string    `gorm:"size:128;not null"`
	SellerRoutingNumber int64     `gorm:"not null"`
	SellerID            string    `gorm:"size:128;not null"`
	OfferJSON           string    `gorm:"type:text;not null"` // serialised contract/sitx.OtcOffer
	Status              string    `gorm:"size:32;not null;default:ongoing;index"`
	CreatedAt           time.Time `gorm:"not null"`
	UpdatedAt           time.Time `gorm:"not null"`
}
