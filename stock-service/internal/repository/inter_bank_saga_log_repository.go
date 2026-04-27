package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/contract/shared"
	"github.com/exbanka/stock-service/internal/model"
)

type InterBankSagaLogRepository struct{ db *gorm.DB }

func NewInterBankSagaLogRepository(db *gorm.DB) *InterBankSagaLogRepository {
	return &InterBankSagaLogRepository{db: db}
}

// WithContext returns a shallow copy of the repository whose underlying
// *gorm.DB is bound to ctx. Used by callers (e.g., the CrossBankRecorder)
// that need saga-step timeout / cancellation to reach the SQL driver.
func (r *InterBankSagaLogRepository) WithContext(ctx context.Context) *InterBankSagaLogRepository {
	return &InterBankSagaLogRepository{db: r.db.WithContext(ctx)}
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

// Save persists a loaded-then-mutated row through GORM's Save (UPDATE by
// primary key). The InterBankSagaLog.BeforeUpdate hook attaches the
// optimistic-lock WHERE version=? clause and increments Version on the
// caller's struct.
//
// Callers MUST surface optimistic-lock conflicts: when the BeforeUpdate
// WHERE version=? clause matches no row (because another transaction
// already incremented Version), the UPDATE returns RowsAffected==0 and we
// translate that to shared.ErrOptimisticLock so callers can retry or
// compensate per the bank-grade concurrency rules in CLAUDE.md.
//
// We use Select("*").Save(...) intentionally: bare db.Save in GORM
// v1.31.1 falls back to INSERT...ON CONFLICT(id) DO UPDATE when the
// initial UPDATE matches zero rows (finisher_api.go:109-110), which
// would silently overwrite the winner of an optimistic-lock race and
// hide the conflict. Selecting "*" sets the `selectedUpdate` flag in
// GORM's Save and disables that fallback path.
func (r *InterBankSagaLogRepository) Save(row *model.InterBankSagaLog) error {
	res := r.db.Select("*").Save(row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return shared.ErrOptimisticLock
	}
	return nil
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
