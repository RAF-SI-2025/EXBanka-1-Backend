package model

import "time"

// Bank is one peer-bank registry row. The bcrypt hashes are for audit and
// rotation; HMAC verification at runtime uses plaintext keys held in env
// (Spec 3 §8.2 / docs/superpowers/specs/2026-04-24-interbank-2pc-transfers-design.md).
//
// `code` is the 3-digit prefix that identifies a peer (also the first 3 chars
// of every account number on that peer's side). Outbound traffic to a peer is
// signed with `api_key_bcrypted`'s plaintext counterpart from env; inbound
// traffic from that peer is verified with `inbound_api_key_bcrypted`'s
// plaintext counterpart.
type Bank struct {
	Code                  string    `gorm:"primaryKey;size:3"`
	Name                  string    `gorm:"size:128;not null"`
	BaseURL               string    `gorm:"size:256;not null"`
	APIKeyBcrypted        string    `gorm:"size:60;not null"`
	InboundAPIKeyBcrypted string    `gorm:"size:60;not null"`
	PublicKey             string    `gorm:"type:text"`
	Active                bool      `gorm:"not null;default:true"`
	CreatedAt             time.Time `gorm:"not null"`
	UpdatedAt             time.Time `gorm:"not null"`
}

func (Bank) TableName() string { return "banks" }
