package shared

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ErrOptimisticLock is returned when a concurrent modification is detected.
var ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another transaction")

// CheckRowsAffected returns ErrOptimisticLock (wrapped) when the result
// reports zero rows affected, which means another transaction updated
// (and incremented the version of) the same row first.
func CheckRowsAffected(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: row was modified by another transaction", ErrOptimisticLock)
	}
	return nil
}
