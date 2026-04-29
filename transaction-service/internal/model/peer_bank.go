package model

import "time"

// PeerBank is the runtime-editable registry of cross-bank peers used by
// the SI-TX implementation. Admins manage rows via /api/v3/peer-banks
// (gated by peer_banks.manage.any). The bank's API token is stored as
// a bcrypt hash; the plaintext is provided to the create/update RPCs and
// also kept in memory for outbound calls — never persisted in plaintext
// outside this DB row.
type PeerBank struct {
	ID                uint64 `gorm:"primaryKey"`
	BankCode          string `gorm:"uniqueIndex;size:8;not null"` // "222"
	RoutingNumber     int64  `gorm:"uniqueIndex;not null"`        // 222
	BaseURL           string `gorm:"size:256;not null"`
	APITokenBcrypt    string `gorm:"size:128;not null"`
	APITokenPlaintext string `gorm:"size:128;not null"`
	HMACInboundKey    string `gorm:"size:256"`
	HMACOutboundKey   string `gorm:"size:256"`
	// Active uses no `default:true` tag because GORM treats bool zero as
	// "use default" at INSERT, which would silently flip Active=false to
	// true. Callers (admin RPC) always set Active explicitly.
	Active    bool      `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}
