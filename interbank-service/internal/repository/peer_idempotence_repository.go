package repository

import (
	"errors"
	"time"

	"github.com/exbanka/interbank-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PeerIdempotenceRepository is the receiver-side replay cache for SI-TX.
// Insert records the (peer_bank_code, locally_generated_key) tuple along
// with the cached response payload; Lookup returns the cached payload on
// replay (found=true) or signals miss (found=false).
type PeerIdempotenceRepository struct {
	db *gorm.DB
}

func NewPeerIdempotenceRepository(db *gorm.DB) *PeerIdempotenceRepository {
	return &PeerIdempotenceRepository{db: db}
}

// Insert atomically writes one record. Caller is expected to invoke this
// inside the same DB tx as any local TX side-effects (per SI-TX §"R must
// record the idempotence key and commit the local part of a transaction
// before sending a response").
func (r *PeerIdempotenceRepository) Insert(rec *model.PeerIdempotenceRecord) error {
	return r.db.Create(rec).Error
}

// UpsertDone writes (or overwrites) the record as status="done" with the
// cached vote + debits/options/meta. Replaces the plain Insert on the cache
// path; on the 202-async path it overwrites the pending row left by the
// timeout. Keyed on (peer_bank_code, locally_generated_key).
func (r *PeerIdempotenceRepository) UpsertDone(rec *model.PeerIdempotenceRecord) error {
	rec.Status = "done"
	if rec.DebitsJSON == "" {
		rec.DebitsJSON = "[]"
	}
	if rec.OptionsJSON == "" {
		rec.OptionsJSON = "[]"
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "peer_bank_code"}, {Name: "locally_generated_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "transaction_id", "response_payload_json", "debits_json",
			"options_json", "message", "payment_code", "payment_purpose",
			"call_number", "tx_routing_number", "tx_foreign_id",
		}),
	}).Create(rec).Error
}

// UpsertPending creates a status="pending" placeholder row iff none exists
// (ON CONFLICT DO NOTHING) — it never clobbers a done row written by a worker
// that finished as the deadline fired. ResponsePayloadJSON gets a "{}"
// placeholder to satisfy NOT NULL.
func (r *PeerIdempotenceRepository) UpsertPending(rec *model.PeerIdempotenceRecord) error {
	rec.Status = "pending"
	if rec.ResponsePayloadJSON == "" {
		rec.ResponsePayloadJSON = "{}"
	}
	if rec.DebitsJSON == "" {
		rec.DebitsJSON = "[]"
	}
	if rec.OptionsJSON == "" {
		rec.OptionsJSON = "[]"
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "peer_bank_code"}, {Name: "locally_generated_key"}},
		DoNothing: true,
	}).Create(rec).Error
}

// Lookup returns (record, true, nil) on hit, (nil, false, nil) on miss,
// or (nil, false, err) on any other error.
func (r *PeerIdempotenceRepository) Lookup(peerBankCode, locallyGeneratedKey string) (*model.PeerIdempotenceRecord, bool, error) {
	var rec model.PeerIdempotenceRecord
	err := r.db.Where("peer_bank_code = ? AND locally_generated_key = ?", peerBankCode, locallyGeneratedKey).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &rec, true, nil
}

// MarkCommitted stamps committed_at on the record (idempotent: only sets it
// when currently NULL). Returns true if THIS call performed the transition
// (committed_at was NULL and is now set), false if it was already committed —
// so the caller can short-circuit a retransmitted COMMIT_TX as a no-op.
func (r *PeerIdempotenceRepository) MarkCommitted(id uint64) (bool, error) {
	res := r.db.Model(&model.PeerIdempotenceRecord{}).
		Where("id = ? AND committed_at IS NULL", id).
		Update("committed_at", time.Now().UTC())
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// MarkRolledBack stamps rolled_back_at on the record (idempotent: only sets it
// when currently NULL). Returns true if THIS call performed the transition,
// false if it was already rolled back — so a retransmitted ROLLBACK_TX is a
// no-op.
func (r *PeerIdempotenceRepository) MarkRolledBack(id uint64) (bool, error) {
	res := r.db.Model(&model.PeerIdempotenceRecord{}).
		Where("id = ? AND rolled_back_at IS NULL", id).
		Update("rolled_back_at", time.Now().UTC())
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// LookupByTransactionID finds a receiver-side record by the INITIATOR's
// transactionId (the ForeignBankId.id the sender assigned to this TX),
// which we persist in the tx_foreign_id column at NEW_TX time.
//
// This is the spec-correct correlation key for COMMIT_TX / ROLLBACK_TX and
// CHECK_STATUS: SI-TX §2.8.2 treats transactionId as INDEPENDENT of the NEW_TX
// idempotence key, so a spec-conformant peer may pick a transactionId.id that
// differs from its NEW_TX locally_generated_key. Correlating by tx_foreign_id
// (not by locally_generated_key, and not by our own receiver-side
// transaction_id UUID) makes inbound COMMIT/ROLLBACK resolve regardless of how
// the peer chose its keys. Indexed by (peer_bank_code, tx_foreign_id).
func (r *PeerIdempotenceRepository) LookupByTransactionID(peerBankCode, transactionID string) (*model.PeerIdempotenceRecord, bool, error) {
	var rec model.PeerIdempotenceRecord
	err := r.db.Where("peer_bank_code = ? AND tx_foreign_id = ?", peerBankCode, transactionID).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &rec, true, nil
}
