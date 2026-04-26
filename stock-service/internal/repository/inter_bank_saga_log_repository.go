package repository

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type InterBankSagaLogRepository struct{ db *gorm.DB }

func NewInterBankSagaLogRepository(db *gorm.DB) *InterBankSagaLogRepository {
	return &InterBankSagaLogRepository{db: db}
}

// UpsertByTxPhaseRole creates or updates the row keyed by (tx_id, phase, role).
// Auto-fills ID and IdempotencyKey on insert. On conflict updates status,
// payload, error_reason, updated_at.
func (r *InterBankSagaLogRepository) UpsertByTxPhaseRole(row *model.InterBankSagaLog) error {
	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.IdempotencyKey == "" {
		row.IdempotencyKey = model.IdempotencyKeyFor(row.SagaKind, row.TxID, row.Phase, row.Role)
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	row.UpdatedAt = time.Now().UTC()
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tx_id"}, {Name: "phase"}, {Name: "role"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "payload_json", "error_reason", "updated_at"}),
	}).Create(row).Error
}

func (r *InterBankSagaLogRepository) Get(txID, phase, role string) (*model.InterBankSagaLog, error) {
	var row model.InterBankSagaLog
	err := r.db.Where("tx_id = ? AND phase = ? AND role = ?", txID, phase, role).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &row, err
}

// ListByTxID returns every row for a saga, ordered chronologically.
func (r *InterBankSagaLogRepository) ListByTxID(txID string) ([]model.InterBankSagaLog, error) {
	var out []model.InterBankSagaLog
	err := r.db.Where("tx_id = ?", txID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// HasInflightForContract returns true if any row for the contract's saga is
// still pending or compensating. Used by crons to avoid double-processing.
func (r *InterBankSagaLogRepository) HasInflightForContract(contractID uint64, sagaKind string) (bool, error) {
	var count int64
	err := r.db.Model(&model.InterBankSagaLog{}).
		Where("contract_id = ? AND saga_kind = ? AND status NOT IN ?",
			contractID, sagaKind, []string{model.IBSagaStatusCompleted, model.IBSagaStatusCompensated}).
		Count(&count).Error
	return count > 0, err
}

// ListStaleByStatus returns rows in `status` older than `staleBefore`. Used
// by the CHECK_STATUS reconciler cron to find phase-2 rows that may have
// silently lost their counterparty's response.
func (r *InterBankSagaLogRepository) ListStaleByStatus(status string, staleBefore time.Time, limit int) ([]model.InterBankSagaLog, error) {
	if limit <= 0 {
		limit = 200
	}
	var out []model.InterBankSagaLog
	err := r.db.Where("status = ? AND updated_at < ?", status, staleBefore).
		Order("updated_at ASC").Limit(limit).Find(&out).Error
	return out, err
}
