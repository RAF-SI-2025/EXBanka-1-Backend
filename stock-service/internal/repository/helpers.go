package repository

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another transaction")

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
