package model

import "time"

// OTCOfferReadReceipt records the most recent updated_at timestamp the user
// has seen for an offer. Drives the unread:bool flag in list responses.
type OTCOfferReadReceipt struct {
	UserID            int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	SystemType        string    `gorm:"primaryKey;size:10" json:"system_type"`
	OfferID           uint64    `gorm:"primaryKey;autoIncrement:false" json:"offer_id"`
	LastSeenUpdatedAt time.Time `gorm:"not null" json:"last_seen_updated_at"`
}
