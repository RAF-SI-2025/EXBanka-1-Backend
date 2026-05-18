package model

import (
	"time"

	"gorm.io/gorm"
)

// OTCOfferReadReceipt records the most recent updated_at timestamp the owner
// has seen for an offer. Drives the unread:bool flag in list responses.
//
// Bank-owned offers store owner_type="bank" with owner_id=0 in the primary
// key (NULL is not allowed in primary keys). The ValidateOwner hook permits
// this exception via OwnerIDForPK; service-layer code must translate
// OwnerType=bank → owner_id=0 when constructing receipts.
type OTCOfferReadReceipt struct {
	OwnerType         OwnerType `gorm:"primaryKey;size:8" json:"owner_type"`
	OwnerID           uint64    `gorm:"primaryKey;autoIncrement:false" json:"owner_id"`
	OfferID           uint64    `gorm:"primaryKey;autoIncrement:false" json:"offer_id"`
	LastSeenUpdatedAt time.Time `gorm:"not null" json:"last_seen_updated_at"`
}

func (r *OTCOfferReadReceipt) BeforeSave(tx *gorm.DB) error {
	if !r.OwnerType.Valid() {
		return ValidateOwner(r.OwnerType, nil)
	}
	if r.OwnerType == OwnerClient && r.OwnerID == 0 {
		return ValidateOwner(OwnerClient, nil)
	}
	return nil
}
