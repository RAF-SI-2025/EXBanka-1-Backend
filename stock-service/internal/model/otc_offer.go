package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OTCOfferStatusPending   = "PENDING"
	OTCOfferStatusCountered = "COUNTERED"
	OTCOfferStatusAccepted  = "ACCEPTED"
	OTCOfferStatusRejected  = "REJECTED"
	OTCOfferStatusExpired   = "EXPIRED"
	OTCOfferStatusFailed    = "FAILED"

	OTCDirectionSellInitiated = "sell_initiated"
	OTCDirectionBuyInitiated  = "buy_initiated"

	OTCActionCreate  = "CREATE"
	OTCActionCounter = "COUNTER"
	OTCActionAccept  = "ACCEPT"
	OTCActionReject  = "REJECT"
)

// OTCOffer is one back-and-forth thread between an initiator and a
// counterparty over a potential option contract. Negotiation history is
// captured by OTCOfferRevision rows; this row holds the current state.
//
// Cross-bank fields (initiator_bank_code, counterparty_bank_code,
// external_correlation_id) stay NULL for intra-bank trades. Spec 4 wires
// them up.
type OTCOffer struct {
	ID                              uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	InitiatorOwnerType              OwnerType       `gorm:"size:8;not null;index:ix_otc_initiator,priority:1;check:initiator_owner_type IN ('client','bank')" json:"initiator_owner_type"`
	InitiatorOwnerID                *uint64         `gorm:"index:ix_otc_initiator,priority:2" json:"initiator_owner_id,omitempty"`
	InitiatorBankCode               *string         `gorm:"size:32" json:"initiator_bank_code,omitempty"`
	CounterpartyOwnerType           *OwnerType      `gorm:"size:8;index:ix_otc_counterparty,priority:1" json:"counterparty_owner_type,omitempty"`
	CounterpartyOwnerID             *uint64         `gorm:"index:ix_otc_counterparty,priority:2" json:"counterparty_owner_id,omitempty"`
	CounterpartyBankCode            *string         `gorm:"size:32" json:"counterparty_bank_code,omitempty"`
	Direction                       string          `gorm:"size:20;not null" json:"direction"`
	StockID                         uint64          `gorm:"not null;index:ix_otc_stock_status,priority:1" json:"stock_id"`
	Quantity                        decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"quantity"`
	StrikePrice                     decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
	Premium                         decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
	SettlementDate                  time.Time       `gorm:"type:date;not null" json:"settlement_date"`
	Status                          string          `gorm:"size:16;not null;index:ix_otc_stock_status,priority:2;index:ix_otc_initiator,priority:3;index:ix_otc_counterparty,priority:3" json:"status"`
	// LastModifiedByPrincipalType / ID is the principal (NOT owner) who last
	// touched the offer. For employee-on-behalf actions this is the employee
	// principal; the owner remains the client. Audit field only.
	LastModifiedByPrincipalType string  `gorm:"size:10;not null" json:"last_modified_by_principal_type"`
	LastModifiedByPrincipalID   uint64  `gorm:"not null" json:"last_modified_by_principal_id"`
	ActingEmployeeID            *uint64 `gorm:"index" json:"acting_employee_id,omitempty"`
	ExternalCorrelationID       *string `gorm:"size:64" json:"external_correlation_id,omitempty"`
	// Cross-bank visibility (Spec 4 / Celina 5). Public offers are listed via
	// peer discovery; Private offers are only shown to the named bank in
	// PrivateToBankCode. NULL bank codes mean a same-bank offer.
	Public            bool    `gorm:"not null;default:true" json:"public"`
	Private           bool    `gorm:"not null;default:false" json:"private"`
	PrivateToBankCode *string `gorm:"size:3" json:"private_to_bank_code,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
	Version                  int64           `gorm:"not null;default:0" json:"-"`
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
	return nil
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
