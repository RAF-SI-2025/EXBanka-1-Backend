package model

import "time"

// PeerIdempotenceRecord is the receiver-side replay cache for SI-TX
// `Message<Type>` envelopes. Per SI-TX §"R must record the idempotence
// key and commit the local part of a transaction before sending a
// response", the row is inserted in the same DB tx as the local TX
// commit. Replays of the same (peer_bank_code, locally_generated_key)
// return the cached ResponsePayloadJSON without re-executing.
//
// Composite-unique: (peer_bank_code, locally_generated_key).
type PeerIdempotenceRecord struct {
	ID                  uint64    `gorm:"primaryKey"`
	PeerBankCode        string    `gorm:"size:8;not null;uniqueIndex:idx_peer_idem_keys"`
	LocallyGeneratedKey string    `gorm:"size:128;not null;uniqueIndex:idx_peer_idem_keys"`
	TransactionID       string    `gorm:"size:128;not null"`
	ResponsePayloadJSON string    `gorm:"type:text;not null"`
	CreatedAt           time.Time `gorm:"not null"`
}
