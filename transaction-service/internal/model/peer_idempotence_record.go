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
	// DebitsJSON is the list of immediate-debits performed during the
	// vote-YES phase of NEW_TX (one entry per DEBIT posting on this
	// bank's routing). Persisted so HandleRollbackTx can credit each
	// debited account back when the IB sends ROLLBACK_TX. Empty (`[]`)
	// when no DEBIT postings landed on this bank — common case for
	// transfers where the sender's bank handles its own debit
	// pre-flight via InitiateOutboundTx, leaving only CREDIT postings
	// for the receiver.
	DebitsJSON string    `gorm:"type:text;not null;default:'[]'"`
	CreatedAt  time.Time `gorm:"not null"`
}
