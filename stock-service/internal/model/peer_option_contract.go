package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// PeerOptionContract is the cross-bank option contract written by
// stock-service at COMMIT_TX time when transaction-service finishes
// the option-formation TX (Celina 5 §3.6 OTC accept). One row per
// option-asset posting that landed on this bank — typically one row
// for the seller's bank (DEBIT direction) and one row for the
// buyer's bank (CREDIT direction).
//
// Distinct from OptionContract (the intra-bank shape, which keys on a
// local OfferID) so cross-bank contracts can be persisted without
// reusing OfferID semantics that don't apply when the negotiation
// row lives on the other bank. A future consolidation can fold these
// into the same table or surface them through unified portfolio
// queries; for now they are recorded as their own ledger of cross-
// bank option formations.
type PeerOptionContract struct {
	ID                       uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	CrossbankTxID            string          `gorm:"size:160;not null;uniqueIndex:ux_peer_oc_keys,priority:1" json:"crossbank_tx_id"`
	PostingIndex             int32           `gorm:"not null;uniqueIndex:ux_peer_oc_keys,priority:2" json:"posting_index"`
	NegotiationRoutingNumber int64           `gorm:"not null" json:"negotiation_routing_number"`
	NegotiationID            string          `gorm:"size:128;not null;index" json:"negotiation_id"`
	BuyerRoutingNumber       int64           `gorm:"not null;index:ix_peer_oc_buyer,priority:1" json:"buyer_routing_number"`
	BuyerID                  string          `gorm:"size:128;not null;index:ix_peer_oc_buyer,priority:2" json:"buyer_id"`
	SellerRoutingNumber      int64           `gorm:"not null;index:ix_peer_oc_seller,priority:1" json:"seller_routing_number"`
	SellerID                 string          `gorm:"size:128;not null;index:ix_peer_oc_seller,priority:2" json:"seller_id"`
	Ticker                   string          `gorm:"size:32;not null" json:"ticker"`
	Quantity                 int64           `gorm:"not null" json:"quantity"`
	StrikePrice              decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Currency                 string          `gorm:"size:8;not null" json:"currency"`
	SettlementDate           string          `gorm:"size:64;not null" json:"settlement_date"`
	// Direction of the original posting that produced this row:
	// "DEBIT" → this bank holds the seller side; "CREDIT" → this
	// bank holds the buyer side. Distinguishing matters for
	// downstream UX (the seller's bank may want to lock holdings,
	// the buyer's bank may want to surface the option in the
	// buyer's portfolio).
	Direction string    `gorm:"size:8;not null" json:"direction"`
	Status    string    `gorm:"size:16;not null;default:'active'" json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (PeerOptionContract) TableName() string { return "peer_option_contracts" }
