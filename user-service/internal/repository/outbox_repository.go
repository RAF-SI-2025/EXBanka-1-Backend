package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/user-service/internal/model"
)

type OutboxRepository struct {
	db *gorm.DB
}

func NewOutboxRepository(db *gorm.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) DB() *gorm.DB { return r.db }

// Insert writes a new outbox row. Caller passes the same *gorm.DB (or *gorm.DB
// transaction) that's running the originating state change so both commits
// atomically.
func (r *OutboxRepository) Insert(e *model.OutboxEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	return r.db.Create(e).Error
}

// InsertTx inserts using a caller-provided transaction.
func (r *OutboxRepository) InsertTx(tx *gorm.DB, e *model.OutboxEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	return tx.Create(e).Error
}

// ClaimUnpublished returns up to limit rows where PublishedAt IS NULL,
// oldest first. MVP polls without row locks; switch to FOR UPDATE SKIP
// LOCKED if multiple relay goroutines run.
func (r *OutboxRepository) ClaimUnpublished(limit int) ([]model.OutboxEvent, error) {
	var out []model.OutboxEvent
	err := r.db.Where("published_at IS NULL").
		Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

func (r *OutboxRepository) MarkPublished(id uint64, at time.Time) error {
	res := r.db.Model(&model.OutboxEvent{}).Where("id = ?", id).Update("published_at", at)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
