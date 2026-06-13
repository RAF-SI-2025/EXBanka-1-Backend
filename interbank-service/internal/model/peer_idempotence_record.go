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
	ID                  uint64 `gorm:"primaryKey"`
	PeerBankCode        string `gorm:"size:8;not null;uniqueIndex:idx_peer_idem_keys;index:idx_peer_idem_txid,priority:1"`
	LocallyGeneratedKey string `gorm:"size:128;not null;uniqueIndex:idx_peer_idem_keys"`
	TransactionID       string `gorm:"size:128;not null"`
	ResponsePayloadJSON string `gorm:"type:text;not null"`
	// DebitsJSON is the list of immediate-debits performed during the
	// vote-YES phase of NEW_TX (one entry per DEBIT posting on this
	// bank's routing). Persisted so HandleRollbackTx can credit each
	// debited account back when the IB sends ROLLBACK_TX. Empty (`[]`)
	// when no DEBIT postings landed on this bank — common case for
	// transfers where the sender's bank handles its own debit
	// pre-flight via InitiateOutboundTx, leaving only CREDIT postings
	// for the receiver.
	DebitsJSON string `gorm:"type:text;not null;default:'[]'"`
	// OptionsJSON is the list of option-asset postings on this bank's
	// routing (one entry per option leg in the original NEW_TX).
	// Materialised into peer_option_contracts rows by HandleCommitTx
	// via stock-service.PeerOTCService.RecordOptionContract; left
	// untouched by HandleRollbackTx since no contract was written at
	// vote time. Empty (`[]`) for non-OTC TXs.
	OptionsJSON string `gorm:"type:text;not null;default:'[]'"`
	// Transaction metadata carried on the NEW_TX envelope (SI-TX §2.8.2),
	// persisted so HandleCommitTx can surface Message as the ledger entry
	// description, and so receiver-side reporting can show the payment code /
	// purpose / call number the initiator sent.
	Message        string `gorm:"type:text;not null;default:''"`
	PaymentCode    string `gorm:"size:64;not null;default:''"`
	PaymentPurpose string `gorm:"type:text;not null;default:''"`
	CallNumber     string `gorm:"size:64;not null;default:''"`
	// TxRoutingNumber / TxForeignID are the initiator's transactionId
	// (ForeignBankId) carried on the NEW_TX. COMMIT_TX / ROLLBACK_TX correlate
	// to this NEW_TX by transactionId — NOT by their own (per-message-unique)
	// idempotence key — so the receiver resolves this record via TxForeignID.
	//
	// (peer_bank_code, tx_foreign_id) is indexed (NON-unique) so the COMMIT_TX /
	// ROLLBACK_TX correlation lookup is O(index). Non-unique because the SI-TX
	// spec (§2.8.2) does not guarantee a peer picks a globally-unique
	// transactionId.id distinct from any other field — only that a spec-conformant
	// initiator keeps it stable for a given TX. A unique index would risk
	// rejecting otherwise-valid NEW_TX inserts.
	TxRoutingNumber int64  `gorm:"not null;default:0"`
	TxForeignID     string `gorm:"size:128;not null;default:'';index:idx_peer_idem_txid,priority:2"`
	// CommittedAt / RolledBackAt make COMMIT_TX / ROLLBACK_TX idempotent on
	// retransmit: once set, a re-delivered COMMIT/ROLLBACK (which carries a
	// fresh idempotence key each time) short-circuits as a no-op instead of
	// re-applying the settle/release.
	CommittedAt  *time.Time
	RolledBackAt *time.Time
	// Status is "pending" while a 202-async reserve runs in the background,
	// or "done" once the vote is cached. Synchronous (fast-path) inserts go
	// straight to "done". Existing rows default to "done".
	Status    string    `gorm:"size:16;not null;default:'done'"`
	CreatedAt time.Time `gorm:"not null"`
}
