package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OptionContractStatusActive    = "ACTIVE"
	OptionContractStatusExercised = "EXERCISED"
	OptionContractStatusExpired   = "EXPIRED"
	OptionContractStatusFailed    = "FAILED"
)

// OptionContract is the executed option that the premium-payment saga
// produces from an OTCOffer. quantity, strike_price, and settlement_date are
// snapshotted from the final accepted revision.
type OptionContract struct {
	ID uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	// RoutingNumber is the bank that owns this row; stamped to OwnRouting on
	// local create by BeforeCreate. (routing_number, native_id) is the
	// bank-scoped natural key + the which-peer / its-foreign-id remote concern.
	// It is NO LONGER the local-vs-remote discriminator — see Local below.
	RoutingNumber int64   `gorm:"not null;default:0;uniqueIndex:ux_oc_native,priority:1" json:"routing_number"`
	NativeID      *string `gorm:"size:128;uniqueIndex:ux_oc_native,priority:2" json:"native_id,omitempty"`
	// Local is THE authoritative local-vs-remote discriminator: true ⇔ this bank
	// hosts the row (RoutingNumber == OwnRouting()), false ⇔ a remote mirror of a
	// peer's row. Stamped once in BeforeCreate (after routing is finalized) and
	// never mutated. When true, the remote-only columns (NativeID, Remote*) are
	// NULL; when false the local-only columns (e.g. OfferID, SagaID) are unused.
	// Queries discriminate on this column.
	Local bool `gorm:"not null;default:false;index" json:"local"`
	// OfferID is the local OTCOffer this contract was minted from. Nullable:
	// remote contracts (added later) have no local offer and store NULL here.
	// Postgres treats NULLs as distinct under a unique index, so the
	// one-contract-per-offer invariant holds for local rows while remote rows
	// (NULL) never collide.
	OfferID         *uint64         `gorm:"uniqueIndex" json:"offer_id,omitempty"`
	BuyerOwnerType  OwnerType       `gorm:"size:8;not null;index:ix_oc_buyer,priority:1;check:buyer_owner_type IN ('client','bank')" json:"buyer_owner_type"`
	BuyerOwnerID    *uint64         `gorm:"index:ix_oc_buyer,priority:2" json:"buyer_owner_id,omitempty"`
	BuyerBankCode   *string         `gorm:"size:32" json:"buyer_bank_code,omitempty"`
	SellerOwnerType OwnerType       `gorm:"size:8;not null;index:ix_oc_seller,priority:1;check:seller_owner_type IN ('client','bank')" json:"seller_owner_type"`
	SellerOwnerID   *uint64         `gorm:"index:ix_oc_seller,priority:2" json:"seller_owner_id,omitempty"`
	SellerBankCode  *string         `gorm:"size:32" json:"seller_bank_code,omitempty"`
	StockID         uint64          `gorm:"not null;index" json:"stock_id"`
	Ticker          string          `gorm:"size:16;not null;default:''" json:"ticker"`
	Quantity        decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice     decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	PremiumPaid     decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium_paid"`
	PremiumCurrency string          `gorm:"size:8;not null" json:"premium_currency"`
	StrikeCurrency  string          `gorm:"size:8;not null" json:"strike_currency"`
	// Buyer-side FX locks. On a cross-currency accept/exercise the buyer's
	// premium/strike is FX-converted to the buyer's account currency; the
	// converted amount is locked here at first run so a crash-recovery of the
	// accept/exercise saga REUSES it instead of re-converting at a drifted rate
	// (which would settle a different amount than the original hold reserved).
	// Zero/empty until the relevant saga sets them.
	BuyerPremiumAmount   decimal.Decimal `gorm:"type:numeric(20,8);not null;default:0" json:"buyer_premium_amount"`
	BuyerPremiumCurrency string          `gorm:"size:8;not null;default:''" json:"buyer_premium_currency"`
	BuyerStrikeAmount    decimal.Decimal `gorm:"type:numeric(20,8);not null;default:0" json:"buyer_strike_amount"`
	BuyerStrikeCurrency  string          `gorm:"size:8;not null;default:''" json:"buyer_strike_currency"`
	SettlementDate       time.Time       `gorm:"type:date;not null;index:ix_oc_settle" json:"settlement_date"`
	// Accounts bound at accept time: buyer's pays the premium/strike, seller's
	// receives them. Read straight off the contract on exercise.
	BuyerAccountID  uint64     `gorm:"not null;default:0" json:"buyer_account_id"`
	SellerAccountID uint64     `gorm:"not null;default:0" json:"seller_account_id"`
	Status          string     `gorm:"size:16;not null;index:ix_oc_buyer,priority:3;index:ix_oc_seller,priority:3" json:"status"`
	SagaID          string     `gorm:"size:64;not null" json:"saga_id"`
	PremiumPaidAt   time.Time  `gorm:"not null" json:"premium_paid_at"`
	ExercisedAt     *time.Time `json:"exercised_at,omitempty"`
	ExpiredAt       *time.Time `json:"expired_at,omitempty"`
	// Cross-bank saga linkage (Spec 4 / Celina 5). CrossbankTxID is set on
	// accept; CrossbankExerciseTxID is set on cross-bank exercise.
	// Sized to hold the NAMESPACED cross-bank tx id "<peerRouting>:<uuid>" (a
	// bare UUID is 36 chars, but the SI-TX correlation prefixes the peer routing,
	// so the stored value runs up to ~56 chars). Matches the holding_reservation
	// CrossbankTxID width (160) so a long peer routing never overflows. Was 36
	// (UUID-only), which made the COMMIT_TX remote-contract write fail on Postgres
	// with "value too long for type character varying(36)" and stranded the
	// cross-bank SI-TX in "committing" with no contract formed.
	CrossbankTxID         *string `gorm:"size:160;index" json:"crossbank_tx_id,omitempty"`
	CrossbankExerciseTxID *string `gorm:"size:160;index" json:"crossbank_exercise_tx_id,omitempty"`
	// OnBehalfOfFundID, when non-nil, records that the BUYER side of this
	// contract was placed on behalf of an investment fund (E2, Plan E).
	// The fund's manager is the acting employee. On exercise, the acquired
	// shares are credited to fund_holdings instead of the buyer's personal
	// holdings.
	OnBehalfOfFundID *uint64 `gorm:"index" json:"on_behalf_of_fund_id,omitempty"`

	// Remote-mirror columns (SP-2a). Populated ONLY on REMOTE rows
	// (routing_number != OwnRouting()), folded in from the retired
	// peer_option_contract mirror. NULL/zero on local rows. The cross-bank
	// option-formation / exercise / expiry flows (RecordOptionContract,
	// InitiateOptionExercise, the SI-TX validators, the daily expiry cron)
	// read & write ONLY these columns + the shared Status column; the local
	// money paths are routing-guarded (Task 3) so they never observe them.
	//
	//   RemotePostingIndex          — the SI-TX posting ordinal that produced
	//                                 this row. (crossbank_tx_id, posting_index)
	//                                 was the retired mirror's natural key; it is
	//                                 preserved inside native_id as
	//                                 "<crossbank_tx_id>:<posting_index>" so
	//                                 UpsertRemoteContract stays idempotent on the
	//                                 (routing_number, native_id) unique index.
	//   RemoteNegotiationRouting /
	//   RemoteNegotiationNativeID   — the originating negotiation reference
	//                                 (OptionDescription.negotiationId). The
	//                                 exercise / money-leg validators look the
	//                                 contract up by this + RemoteDirection.
	//   RemoteDirection             — "DEBIT" (this bank holds the SELLER) or
	//                                 "CREDIT" (this bank holds the BUYER).
	//   RemoteBuyerID / RemoteSellerID — SI-TX participant ids ("client-<n>" /
	//                                 "bank"); the buyer/seller routing live in
	//                                 BuyerBankCode/SellerBankCode (as strings).
	//
	// The shared columns carry the rest: Quantity (decimal of the int qty),
	// StrikePrice, Ticker, StrikeCurrency (the option currency), SettlementDate
	// (parsed time), CrossbankTxID, BuyerBankCode/SellerBankCode (peer routings
	// as strings) and Status (the PEER status vocabulary "active"/"exercised"/
	// "exercising"/"expired" as-is — the SP-1 read shaping tolerates it; local
	// guarded code only ever sees the local uppercase statuses).
	RemotePostingIndex        *int32  `json:"-"`
	RemoteNegotiationRouting  *int64  `gorm:"index:idx_oc_remote_neg,priority:1" json:"-"`
	RemoteNegotiationNativeID *string `gorm:"size:128;index:idx_oc_remote_neg,priority:2" json:"-"`
	RemoteDirection           *string `gorm:"size:8" json:"-"`
	RemoteBuyerID             *string `gorm:"size:128" json:"-"`
	RemoteSellerID            *string `gorm:"size:128" json:"-"`
	// RemoteSellerAccountNumber is the seller's NOMINATED 18-digit account number
	// (the local listing's InitiatorAccountID resolved to its number), stored on a
	// SELLER-side (DEBIT) REMOTE contract at accept-COMMIT. The exercise strike
	// credit reads it back via LookupPeerOptionContract so the strike lands in the
	// account the seller chose rather than their first active <currency> account.
	// NULL ⇒ no nomination stored (older row / unbound) → first-active fallback.
	RemoteSellerAccountNumber *string `gorm:"size:34" json:"-"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `gorm:"not null;default:0" json:"-"`
}

// BeforeCreate stamps the own routing number on local rows. Remote rows
// (added in later tasks) arrive with RoutingNumber already set to the peer's
// routing and are left untouched. Tolerates a nil tx (only touches the struct).
func (c *OptionContract) BeforeCreate(tx *gorm.DB) error {
	if c.RoutingNumber == 0 {
		c.RoutingNumber = OwnRouting()
	}
	// Stamp the discriminator AFTER routing is finalized. Must NOT live in
	// BeforeSave: GORM runs BeforeSave BEFORE BeforeCreate, where routing would
	// still be 0 and a local row would be mis-stamped false.
	c.Local = c.RoutingNumber == OwnRouting()
	return nil
}

func (c *OptionContract) BeforeSave(tx *gorm.DB) error {
	if err := ValidateOwner(c.BuyerOwnerType, c.BuyerOwnerID); err != nil {
		return err
	}
	return ValidateOwner(c.SellerOwnerType, c.SellerOwnerID)
}

func (c *OptionContract) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", c.Version)
	}
	c.Version++
	return nil
}

// IsCrossBank reports whether buyer and seller live on different banks.
// Empty bank codes mean same-bank (legacy / pre-Spec-4 rows).
func (c *OptionContract) IsCrossBank() bool {
	bb, sb := "", ""
	if c.BuyerBankCode != nil {
		bb = *c.BuyerBankCode
	}
	if c.SellerBankCode != nil {
		sb = *c.SellerBankCode
	}
	return bb != "" && sb != "" && bb != sb
}

func (c *OptionContract) IsTerminal() bool {
	switch c.Status {
	case OptionContractStatusExercised, OptionContractStatusExpired, OptionContractStatusFailed:
		return true
	}
	return false
}
