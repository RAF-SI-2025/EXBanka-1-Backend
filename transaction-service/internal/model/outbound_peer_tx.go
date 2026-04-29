package model

import "time"

// OutboundPeerTx is the sender-side state of an outbound SI-TX cross-bank
// transfer. Created by InitiateOutboundTx (Phase 3 Task 10), driven to a
// terminal state (committed | rolled_back | failed) by HandleNewTx's
// dispatch path or by OutboundReplayCron after sender-side crashes.
//
// IdempotenceKey is the SI-TX `locallyGeneratedKey` we send to peer banks;
// it's also the public transaction_id the user polls. Composite-unique
// indexing not required because the key is a UUID.
type OutboundPeerTx struct {
	ID             uint64    `gorm:"primaryKey"`
	IdempotenceKey string    `gorm:"uniqueIndex;size:128;not null"`
	PeerBankCode   string    `gorm:"size:8;not null;index"`
	TxKind         string    `gorm:"size:32;not null"` // "transfer" | "otc-accept" | "otc-exercise"
	PostingsJSON   string    `gorm:"type:text;not null"`
	Status         string    `gorm:"size:32;not null;index"` // pending | committing | committed | rolled_back | failed
	AttemptCount   int       `gorm:"not null;default:0"`
	LastAttemptAt  *time.Time
	LastError      string    `gorm:"size:512"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}
