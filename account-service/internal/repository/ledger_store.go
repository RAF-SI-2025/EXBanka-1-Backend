package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/shared"
)

// LedgerStore adapts the account-service's LedgerEntry table to the
// shared.Store interface so cross-service code (e.g., a saga recovery
// loop) can read ledger rows by idempotency key without importing the
// service-internal model package.
//
// The adapter is intentionally thin: it converts each model.LedgerEntry
// into a shared.Entry on read, and accepts a shared.Entry on write by
// constructing a new model.LedgerEntry. No business logic lives here.
type LedgerStore struct {
	db *gorm.DB
}

// NewLedgerStore wraps a GORM handle into a shared.Store implementation.
func NewLedgerStore(db *gorm.DB) *LedgerStore {
	return &LedgerStore{db: db}
}

// Compile-time guard.
var _ shared.Store = (*LedgerStore)(nil)

// RecordEntry inserts one entry. ErrDuplicateEntry is returned if the
// idempotency key already exists.
func (s *LedgerStore) RecordEntry(ctx context.Context, entry *shared.Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	row := s.toModel(entry)
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		if isDuplicateKey(err) {
			return shared.ErrDuplicateEntry
		}
		return err
	}
	entry.ID = row.ID
	return nil
}

// RecordMovement inserts a debit + credit pair atomically.
func (s *LedgerStore) RecordMovement(ctx context.Context, mv *shared.Movement) error {
	if err := mv.Debit.Validate(); err != nil {
		return err
	}
	if err := mv.Credit.Validate(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		debit := s.toModel(&mv.Debit)
		if err := tx.Create(debit).Error; err != nil {
			if isDuplicateKey(err) {
				return shared.ErrDuplicateEntry
			}
			return err
		}
		mv.Debit.ID = debit.ID
		credit := s.toModel(&mv.Credit)
		if err := tx.Create(credit).Error; err != nil {
			if isDuplicateKey(err) {
				return shared.ErrDuplicateEntry
			}
			return err
		}
		mv.Credit.ID = credit.ID
		return nil
	})
}

// GetByIdempotencyKey returns the entry with the given key, or
// shared.ErrNotFound if no row exists.
func (s *LedgerStore) GetByIdempotencyKey(ctx context.Context, key string) (*shared.Entry, error) {
	if key == "" {
		return nil, shared.ErrNotFound
	}
	var row model.LedgerEntry
	err := s.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.fromModel(&row), nil
}

// ListBySubject returns paginated entries for an account number.
func (s *LedgerStore) ListBySubject(ctx context.Context, subject string, page, pageSize int) ([]shared.Entry, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize
	var rows []model.LedgerEntry
	var total int64
	if err := s.db.WithContext(ctx).Model(&model.LedgerEntry{}).
		Where("account_number = ?", subject).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := s.db.WithContext(ctx).
		Where("account_number = ?", subject).
		Order("created_at DESC").
		Limit(pageSize).Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]shared.Entry, len(rows))
	for i := range rows {
		out[i] = *s.fromModel(&rows[i])
	}
	return out, total, nil
}

// ListByReference returns every entry attached to a (refType, refID)
// pair — both sides of a movement, plus any subsequent fee/interest
// entries that share the reference.
func (s *LedgerStore) ListByReference(ctx context.Context, refType, refID string) ([]shared.Entry, error) {
	var rows []model.LedgerEntry
	err := s.db.WithContext(ctx).
		Where("reference_type = ? AND reference_id = ?", refType, refID).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]shared.Entry, len(rows))
	for i := range rows {
		out[i] = *s.fromModel(&rows[i])
	}
	return out, nil
}

// toModel converts a shared.Entry into the on-disk row shape.
func (s *LedgerStore) toModel(e *shared.Entry) *model.LedgerEntry {
	return &model.LedgerEntry{
		AccountNumber:  e.Subject,
		EntryType:      string(e.Type),
		Amount:         e.Amount,
		BalanceBefore:  e.BalanceBefore,
		BalanceAfter:   e.BalanceAfter,
		Description:    e.Description,
		ReferenceID:    e.ReferenceID,
		ReferenceType:  e.ReferenceType,
		IdempotencyKey: e.IdempotencyKey,
		CreatedAt:      e.CreatedAt,
	}
}

// fromModel converts an on-disk row into the protocol-level Entry.
func (s *LedgerStore) fromModel(r *model.LedgerEntry) *shared.Entry {
	return &shared.Entry{
		ID:             r.ID,
		Subject:        r.AccountNumber,
		Type:           shared.EntryType(r.EntryType),
		Amount:         r.Amount,
		BalanceBefore:  r.BalanceBefore,
		BalanceAfter:   r.BalanceAfter,
		Description:    r.Description,
		ReferenceID:    r.ReferenceID,
		ReferenceType:  r.ReferenceType,
		IdempotencyKey: r.IdempotencyKey,
		CreatedAt:      r.CreatedAt,
	}
}

// isDuplicateKey heuristically detects a unique-constraint violation on
// idempotency_key. Postgres surfaces this as `pq: duplicate key value
// violates unique constraint`, SQLite as `UNIQUE constraint failed`. We
// fall back to substring matching because gorm.io doesn't surface a
// typed sentinel for this.
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "duplicate key") ||
		contains(msg, "UNIQUE constraint failed") ||
		contains(msg, "unique constraint")
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
