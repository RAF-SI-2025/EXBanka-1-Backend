package repository

import (
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"gorm.io/gorm"
)

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

func (r *OutboundPeerTxRepository) MarkCommitted(key string) error {
	return r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ?", key).
		Updates(map[string]interface{}{"status": "committed", "last_error": ""}).Error
}

func (r *OutboundPeerTxRepository) MarkRolledBack(key, reason string) error {
	return r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ?", key).
		Updates(map[string]interface{}{"status": "rolled_back", "last_error": reason}).Error
}

func (r *OutboundPeerTxRepository) MarkFailed(key, reason string) error {
	return r.db.Model(&model.OutboundPeerTx{}).
		Where("idempotence_key = ?", key).
		Updates(map[string]interface{}{"status": "failed", "last_error": reason}).Error
}
