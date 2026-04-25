package repository

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

var (
	// ErrIllegalTransition signals an attempted status transition not allowed
	// by the spec matrix.
	ErrIllegalTransition = errors.New("illegal status transition")
	// ErrConcurrentUpdate signals that the row's status was changed by
	// another concurrent operation between read and write.
	ErrConcurrentUpdate = errors.New("concurrent update — version mismatch")
)

// InterBankTxRepository persists inter_bank_transactions rows and enforces
// the transition matrix at the storage boundary.
type InterBankTxRepository struct {
	db *gorm.DB
}

func NewInterBankTxRepository(db *gorm.DB) *InterBankTxRepository {
	return &InterBankTxRepository{db: db}
}

// Create inserts a new row. Caller must populate Phase, Status, Role, and
// IdempotencyKey before calling.
func (r *InterBankTxRepository) Create(tx *model.InterBankTransaction) error {
	now := time.Now().UTC()
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = now
	}
	tx.UpdatedAt = now
	return r.db.Create(tx).Error
}

// Get returns the row keyed by (tx_id, role) or gorm.ErrRecordNotFound.
func (r *InterBankTxRepository) Get(txID, role string) (*model.InterBankTransaction, error) {
	var t model.InterBankTransaction
	err := r.db.First(&t, "tx_id = ? AND role = ?", txID, role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &t, err
}

// GetByIdempotencyKey looks the row up by its idempotency key.
func (r *InterBankTxRepository) GetByIdempotencyKey(key string) (*model.InterBankTransaction, error) {
	var t model.InterBankTransaction
	err := r.db.First(&t, "idempotency_key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &t, err
}

// UpdateStatus enforces the transition matrix and uses a `WHERE status =
// from` clause for compare-and-swap semantics. Returns ErrIllegalTransition
// if the matrix forbids the move, or ErrConcurrentUpdate if the row's
// current status no longer matches `from` (i.e., somebody else moved it).
func (r *InterBankTxRepository) UpdateStatus(txID, role, from, to string) error {
	if !model.IsValidTransition(from, to) {
		return ErrIllegalTransition
	}
	res := r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ? AND status = ?", txID, role, from).
		Updates(map[string]any{
			"status":     to,
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrConcurrentUpdate
	}
	return nil
}

// SetFinalizedTerms records the FX rate, fees, and final amount/currency
// computed by the receiver at Prepare time. Idempotent on (tx_id, role) — a
// second call with the same values is a no-op for callers.
func (r *InterBankTxRepository) SetFinalizedTerms(
	txID, role string,
	finalAmt decimal.Decimal,
	finalCcy string,
	fxRate decimal.Decimal,
	fees decimal.Decimal,
) error {
	return r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ?", txID, role).
		Updates(map[string]any{
			"amount_final":   finalAmt,
			"currency_final": finalCcy,
			"fx_rate":        fxRate,
			"fees_final":     fees,
			"updated_at":     time.Now().UTC(),
		}).Error
}

// SetErrorReason records the failure reason on a non-terminal row.
func (r *InterBankTxRepository) SetErrorReason(txID, role, reason string) error {
	return r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ?", txID, role).
		Updates(map[string]any{
			"error_reason": reason,
			"updated_at":   time.Now().UTC(),
		}).Error
}

// IncrementRetry bumps retry_count; used by the reconciler.
func (r *InterBankTxRepository) IncrementRetry(txID, role string) error {
	return r.db.Model(&model.InterBankTransaction{}).
		Where("tx_id = ? AND role = ?", txID, role).
		Updates(map[string]any{
			"retry_count": gorm.Expr("retry_count + 1"),
			"updated_at":  time.Now().UTC(),
		}).Error
}

// ListReconcilable returns sender rows in `reconciling` status, FIFO by
// updated_at. Used by the reconciler cron to pick its next batch.
func (r *InterBankTxRepository) ListReconcilable(limit int) ([]model.InterBankTransaction, error) {
	var out []model.InterBankTransaction
	err := r.db.Where("role = ? AND status = ?", model.RoleSender, model.StatusReconciling).
		Order("updated_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListReceiverStaleReadySent returns receiver rows in `ready_sent` whose
// updated_at is older than `staleBefore`. Used by the receiver-timeout cron.
func (r *InterBankTxRepository) ListReceiverStaleReadySent(staleBefore time.Time, limit int) ([]model.InterBankTransaction, error) {
	var out []model.InterBankTransaction
	err := r.db.Where("role = ? AND status = ? AND updated_at < ?",
		model.RoleReceiver, model.StatusReadySent, staleBefore).
		Order("updated_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListNonTerminal returns all rows in non-terminal status — used by the
// startup crash-recovery routine.
func (r *InterBankTxRepository) ListNonTerminal() ([]model.InterBankTransaction, error) {
	var out []model.InterBankTransaction
	terminal := []string{
		model.StatusCommitted, model.StatusRolledBack,
		model.StatusFinalNotReady, model.StatusAbandoned,
	}
	err := r.db.Where("status NOT IN ?", terminal).Find(&out).Error
	return out, err
}
