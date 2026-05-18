package repository

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
)

// scopeOwner adds a (owner_type, owner_id) predicate. ownerID is *uint64
// because bank-owner rows have owner_id = NULL — we MUST emit IS NULL
// rather than `= 0`, which would never match.
//
// Use as: q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID).
func scopeOwner(q *gorm.DB, ownerTypeCol, ownerIDCol string, ownerType model.OwnerType, ownerID *uint64) *gorm.DB {
	q = q.Where(fmt.Sprintf("%s = ?", ownerTypeCol), string(ownerType))
	if ownerID == nil {
		return q.Where(fmt.Sprintf("%s IS NULL", ownerIDCol))
	}
	return q.Where(fmt.Sprintf("%s = ?", ownerIDCol), *ownerID)
}

// ErrOptimisticLock is the typed sentinel returned when a concurrent
// modification is detected. Carries codes.Aborted so service handlers can
// passthrough without an explicit mapping table.
var ErrOptimisticLock = svcerr.New(codes.Aborted, "optimistic lock conflict: record was modified by another transaction")

// CheckRowsAffected returns ErrOptimisticLock when the result has no error but
// zero rows were affected, indicating a concurrent update won the optimistic-
// lock race. It is the repository-layer mirror of shared.CheckRowsAffected.
func CheckRowsAffected(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: row was modified by another transaction", ErrOptimisticLock)
	}
	return nil
}

var allowedSortColumns = map[string]string{
	"price":  "price",
	"volume": "volume",
	"change": "change",
	"margin": "price", // margin is derived from price; sort by price as proxy
}

func applySorting(q *gorm.DB, table, sortBy, sortOrder string) *gorm.DB {
	col, ok := allowedSortColumns[sortBy]
	if !ok {
		col = "price"
	}
	dir := "ASC"
	if sortOrder == "desc" {
		dir = "DESC"
	}
	return q.Order(fmt.Sprintf("%s.%s %s", table, col, dir))
}

// MaxPageSize is the hard ceiling for a single paginated repository call.
// Internal callers (seed/sync loops) may request up to this value; user-facing
// gRPC handlers are already expected to bound requests before reaching the
// repository. This used to be 100 with a silent reset to 10 for larger
// requests, which caused internal seeding code to quietly process only 10
// rows. See `listing_service.syncListings` and `security_sync.generateAllOptions`.
const MaxPageSize = 10000

func applyPagination(q *gorm.DB, page, pageSize int) *gorm.DB {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize < 1:
		pageSize = 10
	case pageSize > MaxPageSize:
		pageSize = MaxPageSize
	}
	return q.Offset((page - 1) * pageSize).Limit(pageSize)
}
