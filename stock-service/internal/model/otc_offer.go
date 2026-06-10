package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	// Legacy status values — used by the pre-Phase-2 negotiation-thread
	// model where OTCOffer rows mutated in-place through a single chain.
	// New code creates listings with OTCOfferStatusOpen instead; helpers
	// below treat PENDING/COUNTERED as synonyms for "open" so existing
	// rows remain fillable through the new path.
	OTCOfferStatusPending   = "PENDING"
	OTCOfferStatusCountered = "COUNTERED"
	OTCOfferStatusAccepted  = "ACCEPTED"
	OTCOfferStatusRejected  = "REJECTED"
	OTCOfferStatusExpired   = "EXPIRED"
	OTCOfferStatusFailed    = "FAILED"

	// New (Phase 2) lifecycle — OTCOffer is an immutable listing.
	//   open      — accepting negotiations
	//   consumed  — one negotiation chain accepted; siblings auto-cancelled
	//   cancelled — poster withdrew before any accept
	OTCOfferStatusOpen      = "open"
	OTCOfferStatusConsumed  = "consumed"
	OTCOfferStatusCancelled = "cancelled"

	OTCDirectionSellInitiated = "sell_initiated"
	OTCDirectionBuyInitiated  = "buy_initiated"

	OTCActionCreate  = "CREATE"
	OTCActionCounter = "COUNTER"
	OTCActionAccept  = "ACCEPT"
	OTCActionReject  = "REJECT"

	// RemoteStockShellPrefix namespaces native_id for shells synthesized from a peer's
	// /public-stock ("ps:<sellerRn>:<sellerId>:<ticker>"), distinct from option-offer rows.
	RemoteStockShellPrefix = "ps:"
)

// IsOpenListing reports whether an OTCOffer is currently accepting
// negotiations. Treats both the legacy thread statuses (PENDING,
// COUNTERED) and the new listing status (open) as fillable. False for
// any terminal status.
func (o *OTCOffer) IsOpenListing() bool {
	switch o.Status {
	case OTCOfferStatusOpen, OTCOfferStatusPending, OTCOfferStatusCountered:
		return true
	}
	return false
}

// OTCOffer is one back-and-forth thread between an initiator and a
// counterparty over a potential option contract. Negotiation history is
// captured by OTCOfferRevision rows; this row holds the current state.
//
// Cross-bank fields (initiator_bank_code, counterparty_bank_code,
// external_correlation_id) stay NULL for intra-bank trades. Spec 4 wires
// them up.
type OTCOffer struct {
	ID uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	// RoutingNumber is the bank that owns this row; stamped to OwnRouting on
	// local create by BeforeCreate. (routing_number, native_id) is the
	// bank-scoped natural key + the which-peer / its-foreign-id remote concern.
	// It is NO LONGER the local-vs-remote discriminator — see Local below.
	RoutingNumber int64   `gorm:"not null;default:0;uniqueIndex:ux_otc_offer_native,priority:1" json:"routing_number"`
	NativeID      *string `gorm:"size:128;uniqueIndex:ux_otc_offer_native,priority:2" json:"native_id,omitempty"`
	// Local is THE authoritative local-vs-remote discriminator: true ⇔ this bank
	// hosts the row (RoutingNumber == OwnRouting()), false ⇔ a remote mirror of a
	// peer's row. Stamped once in BeforeCreate (after routing is finalized) and
	// never mutated. When true, the remote-only columns (NativeID, Remote*, the
	// peer currencies, LastSeenAt) are NULL; when false, the local-only columns
	// (e.g. InitiatorAccountID) are unused. Queries discriminate on this column.
	Local                 bool            `gorm:"not null;default:false;index" json:"local"`
	InitiatorOwnerType    OwnerType       `gorm:"size:8;not null;index:ix_otc_initiator,priority:1;check:initiator_owner_type IN ('client','bank')" json:"initiator_owner_type"`
	InitiatorOwnerID      *uint64         `gorm:"index:ix_otc_initiator,priority:2" json:"initiator_owner_id,omitempty"`
	InitiatorBankCode     *string         `gorm:"size:32" json:"initiator_bank_code,omitempty"`
	CounterpartyOwnerType *OwnerType      `gorm:"size:8;index:ix_otc_counterparty,priority:1" json:"counterparty_owner_type,omitempty"`
	CounterpartyOwnerID   *uint64         `gorm:"index:ix_otc_counterparty,priority:2" json:"counterparty_owner_id,omitempty"`
	CounterpartyBankCode  *string         `gorm:"size:32" json:"counterparty_bank_code,omitempty"`
	Direction             string          `gorm:"size:20;not null" json:"direction"`
	StockID               uint64          `gorm:"not null;index:ix_otc_stock_status,priority:1" json:"stock_id"`
	Ticker                string          `gorm:"size:16;not null;default:''" json:"ticker"`
	Quantity              decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice           decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium               decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate        time.Time       `gorm:"type:date;not null" json:"settlement_date"`
	Status                string          `gorm:"size:16;not null;index:ix_otc_stock_status,priority:2;index:ix_otc_initiator,priority:3;index:ix_otc_counterparty,priority:3" json:"status"`
	// LastModifiedByPrincipalType / ID is the principal (NOT owner) who last
	// touched the offer. For employee-on-behalf actions this is the employee
	// principal; the owner remains the client. Audit field only.
	LastModifiedByPrincipalType string `gorm:"size:10;not null" json:"last_modified_by_principal_type"`
	LastModifiedByPrincipalID   uint64 `gorm:"not null" json:"last_modified_by_principal_id"`
	// ActingEmployeeID is the employee who ORIGINATED this bank-owned resource.
	// Non-nil ONLY when the owner is the bank and an employee created it. It is the
	// STABLE SI-TX wire-identity source: the bank party publishes as
	// "employee-<ActingEmployeeID>" on every wire action, regardless of which
	// employee performs later actions. nil for client-owned resources and legacy/seed bank rows.
	ActingEmployeeID      *uint64 `gorm:"index" json:"acting_employee_id,omitempty"`
	ExternalCorrelationID *string `gorm:"size:64" json:"external_correlation_id,omitempty"`
	// InitiatorAccountID is the initiator's account bound at offer creation:
	// it pays the premium on buy_initiated offers and receives it on
	// sell_initiated offers. The counterparty's account is bound at accept.
	InitiatorAccountID uint64 `gorm:"not null;default:0" json:"initiator_account_id"`
	// Cross-bank visibility (Spec 4 / Celina 5). Public offers are listed via
	// peer discovery; Private offers are only shown to the named bank in
	// PrivateToBankCode. NULL bank codes mean a same-bank offer.
	Public            bool    `gorm:"not null;default:true" json:"public"`
	Private           bool    `gorm:"not null;default:false" json:"private"`
	PrivateToBankCode *string `gorm:"size:3" json:"private_to_bank_code,omitempty"`
	// Remote-mirror columns (SP-2a). Populated ONLY on remote rows
	// (routing_number != OwnRouting()), folded in from the retired
	// remote_otc_offer mirror. NULL on local rows.
	//   StrikeCurrency / PremiumCurrency — the peer's published currencies
	//     (OTCOffer carries no currency for local rows; it lives on the
	//     StockExchange the listing trades on).
	//   RemoteSellerID — the SI-TX wire seller id ("client-<N>" | "bank").
	//   LastSeenAt — last successful peer poll that listed the offer; the
	//     reconcile flip (open->cancelled) keys off it.
	StrikeCurrency  *string `gorm:"size:8" json:"strike_currency,omitempty"`
	PremiumCurrency *string `gorm:"size:8" json:"premium_currency,omitempty"`
	RemoteSellerID  *string `gorm:"size:128" json:"remote_seller_id,omitempty"`
	// HasPresetTerms: true ⇔ the offer carries owner-set terms (a /public-option-offers
	// listing — a negotiable "start position"). false ⇔ synthesized from /public-stock
	// (no preset strike/premium; fully buyer-negotiated).
	HasPresetTerms bool       `gorm:"not null;default:true" json:"has_preset_terms"`
	LastSeenAt     *time.Time `gorm:"index" json:"last_seen_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Version        int64      `gorm:"not null;default:0" json:"-"`
}

// BeforeCreate stamps the own routing number on local rows. Remote rows
// (added in later tasks) arrive with RoutingNumber already set to the peer's
// routing and are left untouched. Tolerates a nil tx (only touches the struct).
func (o *OTCOffer) BeforeCreate(tx *gorm.DB) error {
	if o.RoutingNumber == 0 {
		o.RoutingNumber = OwnRouting()
	}
	// Stamp the discriminator AFTER routing is finalized. Must NOT live in
	// BeforeSave: GORM runs BeforeSave BEFORE BeforeCreate, where routing would
	// still be 0 and a local row would be mis-stamped false.
	o.Local = o.RoutingNumber == OwnRouting()
	return nil
}

func (o *OTCOffer) BeforeSave(tx *gorm.DB) error {
	if err := ValidateOwner(o.InitiatorOwnerType, o.InitiatorOwnerID); err != nil {
		return err
	}
	if o.CounterpartyOwnerType != nil {
		if err := ValidateOwner(*o.CounterpartyOwnerType, o.CounterpartyOwnerID); err != nil {
			return err
		}
	}
	return ValidateActingEmployee(o.InitiatorOwnerType, o.ActingEmployeeID)
}

func (o *OTCOffer) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", o.Version)
	}
	o.Version++
	return nil
}

// IsCrossBankOffer reports whether the offer participates in a cross-bank
// trade from the perspective of the given bank. Empty / NULL bank-code
// columns mean the row predates Spec 4 and is treated as same-bank.
func IsCrossBankOffer(o *OTCOffer, selfBankCode string) bool {
	cpb := ""
	if o.CounterpartyBankCode != nil {
		cpb = *o.CounterpartyBankCode
	}
	ipb := ""
	if o.InitiatorBankCode != nil {
		ipb = *o.InitiatorBankCode
	}
	if cpb == "" && ipb == "" {
		return false
	}
	if ipb != "" && ipb != selfBankCode {
		return true
	}
	if cpb != "" && cpb != selfBankCode {
		return true
	}
	return false
}

// IsTerminal reports whether the offer is in a terminal state and cannot be
// counter-ed, accepted, or rejected.
func (o *OTCOffer) IsTerminal() bool {
	switch o.Status {
	case OTCOfferStatusAccepted, OTCOfferStatusRejected, OTCOfferStatusExpired, OTCOfferStatusFailed:
		return true
	}
	return false
}
