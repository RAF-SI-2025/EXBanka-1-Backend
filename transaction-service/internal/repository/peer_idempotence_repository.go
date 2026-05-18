package repository

import (
	"errors"

	"github.com/exbanka/transaction-service/internal/model"
	"gorm.io/gorm"
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
