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

func applyPagination(q *gorm.DB, page, pageSize int) *gorm.DB {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	return q.Offset((page - 1) * pageSize).Limit(pageSize)
}
