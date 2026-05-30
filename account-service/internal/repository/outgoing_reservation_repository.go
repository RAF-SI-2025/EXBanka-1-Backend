package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
)

// OutgoingReservationRepository persists debit-side reservation rows for
// cross-bank (SI-TX) money DEBIT legs. Lifecycle: pending → (settled | released).
// Debit-side mirror of IncomingReservationRepository.
type OutgoingReservationRepository struct {
	db *gorm.DB
}

func NewOutgoingReservationRepository(db *gorm.DB) *OutgoingReservationRepository {
	return &OutgoingReservationRepository{db: db}
}

// WithTx returns a repository bound to the given transaction handle.
func (r *OutgoingReservationRepository) WithTx(tx *gorm.DB) *OutgoingReservationRepository {
	return &OutgoingReservationRepository{db: tx}
}

func (r *OutgoingReservationRepository) Create(res *model.OutgoingReservation) error {
	return r.db.Create(res).Error
}

// GetByKey returns the reservation row for the given key, or
// gorm.ErrRecordNotFound if missing.
func (r *OutgoingReservationRepository) GetByKey(key string) (*model.OutgoingReservation, error) {
	var res model.OutgoingReservation
	err := r.db.Where("reservation_key = ?", key).First(&res).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &res, err
}

// MarkSettled transitions a pending reservation to settled. Idempotent on
// non-pending rows (RowsAffected = 0). Caller must already have applied the
// Balance debit + ledger entry in the same transaction.
func (r *OutgoingReservationRepository) MarkSettled(tx *gorm.DB, key string) error {
	res := tx.Model(&model.OutgoingReservation{}).
		Where("reservation_key = ? AND status = ?", key, model.OutgoingReservationStatusPending).
		Updates(map[string]any{
			"status":     model.OutgoingReservationStatusSettled,
			"updated_at": time.Now().UTC(),
		})
	return res.Error
}

// MarkReleased transitions a pending reservation to released. Idempotent on
// non-pending rows.
func (r *OutgoingReservationRepository) MarkReleased(tx *gorm.DB, key string) error {
	res := tx.Model(&model.OutgoingReservation{}).
		Where("reservation_key = ? AND status = ?", key, model.OutgoingReservationStatusPending).
		Updates(map[string]any{
			"status":     model.OutgoingReservationStatusReleased,
			"updated_at": time.Now().UTC(),
		})
	return res.Error
}

// ListStalePendingOlderThan returns pending reservations created before
// `before`. Drives the timeout cron that releases holds whose peer bank never
// sent COMMIT/ROLLBACK (time-safety backstop).
func (r *OutgoingReservationRepository) ListStalePendingOlderThan(before time.Time, limit int) ([]model.OutgoingReservation, error) {
	var out []model.OutgoingReservation
	err := r.db.Where("status = ? AND created_at < ?", model.OutgoingReservationStatusPending, before).
		Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}
