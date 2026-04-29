package repository

import (
	"errors"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/credit-service/internal/model"
)

// IdempotencyRepository persists per-key cached responses for saga-driven
// RPCs. Concurrent claims of the same key are resolved by PostgreSQL's
// ON CONFLICT DO NOTHING — the loser fetches the winner's cached response.
type IdempotencyRepository struct {
	db *gorm.DB
}

// NewIdempotencyRepository wires the repository against a *gorm.DB. The DB
// passed here is only used as a default; Run takes an explicit transaction
// so the caller can scope the claim to their own outer TX.
func NewIdempotencyRepository(db *gorm.DB) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

// Run executes fn under the given idempotency key. The first call with a
// given key runs fn and caches the marshalled response. Subsequent calls
// with the same key skip fn and return the cached response unmarshalled
// into a fresh instance produced by newT.
//
// The caller passes the active *gorm.DB transaction; Run uses
// ON CONFLICT DO NOTHING for the claim so concurrent calls with the same
// key do not deadlock.
//
// Run is a package-level function (not a method) because Go does not
// allow type parameters on methods; the *IdempotencyRepository receiver
// is passed explicitly.
func Run[T proto.Message](r *IdempotencyRepository, tx *gorm.DB, key string,
	newT func() T, fn func() (T, error)) (T, error) {

	var zero T
	if key == "" {
		return zero, errors.New("idempotency key required")
	}

	// Try to claim the key. Concurrent claimants race; the loser sees
	// RowsAffected == 0 and falls through to the cache-read branch.
	// ResponseBlob is initialised to a non-nil empty slice so the
	// "not null" column constraint is satisfied at INSERT time; the
	// real payload is written immediately below once fn returns.
	rec := model.IdempotencyRecord{Key: key, ResponseBlob: []byte{}}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec)
	if res.Error != nil {
		return zero, res.Error
	}

	if res.RowsAffected == 0 {
		// Existing record — fetch and return cached response.
		var existing model.IdempotencyRecord
		if err := tx.First(&existing, "key = ?", key).Error; err != nil {
			return zero, err
		}
		target := newT()
		if err := proto.Unmarshal(existing.ResponseBlob, target); err != nil {
			return zero, err
		}
		return target, nil
	}

	// Fresh — execute the business logic.
	resp, err := fn()
	if err != nil {
		// Roll back the claim so retries can re-execute. Best-effort
		// delete; if it fails (e.g., outer TX already aborted), the
		// caller's surrounding transaction will roll the row back too.
		_ = tx.Delete(&model.IdempotencyRecord{}, "key = ?", key).Error
		return zero, err
	}
	blob, mErr := proto.Marshal(resp)
	if mErr != nil {
		return zero, mErr
	}
	if err := tx.Model(&rec).Update("response_blob", blob).Error; err != nil {
		return zero, err
	}
	return resp, nil
}
