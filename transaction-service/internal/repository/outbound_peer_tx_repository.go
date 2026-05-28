package repository

import (
	"errors"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"gorm.io/gorm"
)

// ErrPeerTxAlreadyResolved is returned by MarkCommitted and MarkRolledBack when
// the row is no longer in "pending" status. This signals a concurrent resolution
// race (e.g., OutboundReplayCron and PeerTxReconciler racing on the same row).
// Callers must check for this sentinel and skip any subsequent side-effects
// (such as localReverse) to avoid double-crediting the sender.
var ErrPeerTxAlreadyResolved = errors.New("outbound peer tx already resolved")

// OutboundPeerTxRepository is sender-side state for cross-bank SI-TX
// transfers. Read by OutboundReplayCron, written by InitiateOutboundTx
// + the dispatch path's status updates.
type OutboundPeerTxRepository struct {
	db *gorm.DB
}

func NewOutboundPeerTxRepository(db *gorm.DB) *OutboundPeerTxRepository {
	return &OutboundPeerTxRepository{db: db}
}

func (r *OutboundPeerTxRepository) Create(row *model.OutboundPeerTx) error {
	return r.db.Create(row).Error
}

func (r *OutboundPeerTxRepository) GetByIdempotenceKey(key string) (*model.OutboundPeerTx, error) {
	var row model.OutboundPeerTx
	if err := r.db.Where("idempotence_key = ?", key).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ListPendingOlderThan returns pending rows whose last_attempt_at is
// before cutoff (or NULL — never attempted). Used by OutboundReplayCron
// to find rows that need a retry POST.
func (r *OutboundPeerTxRepository) ListPendingOlderThan(cutoff time.Time) ([]model.OutboundPeerTx, error) {
	var rows []model.OutboundPeerTx
	err := r.db.
		Where("status = ? AND (last_attempt_at IS NULL OR last_attempt_at < ?)", "pending", cutoff).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// MarkAttempt increments attempt_count and stamps last_attempt_at + last_error.
// Status is unchanged (still "pending"). Called after every dispatch attempt
// regardless of outcome — terminal status changes are MarkCommitted /
// MarkRolledBack / MarkFailed.
func (r *OutboundPeerTxRepository) MarkAttempt(key, errMsg string) error {
	now := time.Now().UTC()
	return r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ?", key).
		Updates(map[string]interface{}{
			"attempt_count":   gorm.Expr("attempt_count + 1"),
			"last_attempt_at": &now,
			"last_error":      errMsg,
		}).Error
}

// MarkCommitted transitions a pending row to committed. The UPDATE is scoped
// to "status = 'pending'" to guard against a concurrent resolution race
// (e.g., OutboundReplayCron and PeerTxReconciler both acting on the same row).
// Returns ErrPeerTxAlreadyResolved when RowsAffected == 0 (race lost or row
// already in a terminal state).
func (r *OutboundPeerTxRepository) MarkCommitted(key string) error {
	result := r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ? AND status = 'pending'", key).
		Updates(map[string]interface{}{"status": "committed", "last_error": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPeerTxAlreadyResolved
	}
	return nil
}

// MarkRolledBack transitions a pending row to rolled_back. The UPDATE is
// scoped to "status = 'pending'" for the same race-guard as MarkCommitted.
// Callers MUST call this BEFORE calling localReverse: if MarkRolledBack
// returns ErrPeerTxAlreadyResolved the reversal must be skipped entirely.
// Returns ErrPeerTxAlreadyResolved when RowsAffected == 0.
func (r *OutboundPeerTxRepository) MarkRolledBack(key, reason string) error {
	result := r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ? AND status = 'pending'", key).
		Updates(map[string]interface{}{"status": "rolled_back", "last_error": reason})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPeerTxAlreadyResolved
	}
	return nil
}

func (r *OutboundPeerTxRepository) MarkFailed(key, reason string) error {
	return r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ?", key).
		Updates(map[string]interface{}{"status": "failed", "last_error": reason}).Error
}
