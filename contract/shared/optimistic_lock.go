package shared

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/svcerr"
)

// ErrOptimisticLock is returned when a concurrent modification is detected.
//
// It is a typed sentinel that carries codes.Aborted via the GRPCStatus
// interface, so service code that returns (or wraps) this sentinel maps
// automatically to a gRPC Aborted status without an explicit handler-side
// translation.
var ErrOptimisticLock = svcerr.New(codes.Aborted, "optimistic lock conflict: record was modified by another transaction")

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
