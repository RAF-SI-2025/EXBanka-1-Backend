package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
)

// IncomingReservationRepository persists credit-side reservation rows for
// inter-bank inbound transfers. Lifecycle: pending → (committed | released).
type IncomingReservationRepository struct {
	db *gorm.DB
}

func NewIncomingReservationRepository(db *gorm.DB) *IncomingReservationRepository {
	return &IncomingReservationRepository{db: db}
}

// WithTx returns a repository bound to the given transaction handle.
func (r *IncomingReservationRepository) WithTx(tx *gorm.DB) *IncomingReservationRepository {
	return &IncomingReservationRepository{db: tx}
}

// Create inserts a new pending reservation. The unique index on
// reservation_key surfaces duplicate inserts as an error; callers that need
// idempotency-on-replay should call GetByKey first.
func (r *IncomingReservationRepository) Create(res *model.IncomingReservation) error {
	return r.db.Create(res).Error
}

// GetByKey returns the reservation row for the given key, or
// gorm.ErrRecordNotFound if missing.
func (r *IncomingReservationRepository) GetByKey(key string) (*model.IncomingReservation, error) {
	var res model.IncomingReservation
	err := r.db.Where("reservation_key = ?", key).First(&res).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &res, err
}

// MarkCommitted transitions a pending reservation to committed. Idempotent:
// calling on an already-committed row is a no-op (RowsAffected = 0).
func (r *IncomingReservationRepository) MarkCommitted(key string) error {
	res := r.db.Model(&model.IncomingReservation{}).
		Where("reservation_key = ? AND status = ?", key, model.IncomingReservationStatusPending).
		Updates(map[string]any{
			"status":     model.IncomingReservationStatusCommitted,
			"updated_at": time.Now().UTC(),
		})
	return res.Error
}

// MarkReleased transitions a pending reservation to released. Idempotent on
// non-pending rows (RowsAffected = 0).
func (r *IncomingReservationRepository) MarkReleased(key string) error {
	res := r.db.Model(&model.IncomingReservation{}).
		Where("reservation_key = ? AND status = ?", key, model.IncomingReservationStatusPending).
		Updates(map[string]any{
			"status":     model.IncomingReservationStatusReleased,
			"updated_at": time.Now().UTC(),
		})
	return res.Error
}

// ListStaleByCreatedBefore returns pending reservations older than `before`.
// Operational/debug surface; the receiver-timeout cron in transaction-service
// works directly on inter_bank_transactions, but this is exposed for
// reconciliation tooling.
func (r *IncomingReservationRepository) ListStaleByCreatedBefore(before time.Time, limit int) ([]model.IncomingReservation, error) {
	var out []model.IncomingReservation
	err := r.db.Where("status = ? AND created_at < ?", model.IncomingReservationStatusPending, before).
		Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}
