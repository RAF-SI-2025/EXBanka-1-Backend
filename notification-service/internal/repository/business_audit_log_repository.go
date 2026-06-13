package repository

import (
	"time"

	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
)

// BusinessAuditLogRepository provides read access to the business_audit_logs table.
type BusinessAuditLogRepository struct {
	db *gorm.DB
}

func NewBusinessAuditLogRepository(db *gorm.DB) *BusinessAuditLogRepository {
	return &BusinessAuditLogRepository{db: db}
}

// BusinessAuditLogFilters holds optional filters for ListAll.
type BusinessAuditLogFilters struct {
	Since      int64  // unix seconds, 0 = no lower bound
	Until      int64  // unix seconds, 0 = no upper bound
	ActorID    int64  // actor employee_id, 0 = all
	Action     string // exact match, "" = all
	TargetType string // exact match, "" = all
}

// ListAll returns paginated business audit log rows ordered by timestamp DESC.
// Filters are all optional.
func (r *BusinessAuditLogRepository) ListAll(filters BusinessAuditLogFilters, page, pageSize int) ([]model.BusinessAuditLog, int64, error) {
	var entries []model.BusinessAuditLog
	var total int64

	query := r.db.Model(&model.BusinessAuditLog{})
	if filters.Since > 0 {
		query = query.Where("timestamp >= ?", time.Unix(filters.Since, 0))
	}
	if filters.Until > 0 {
		query = query.Where("timestamp <= ?", time.Unix(filters.Until, 0))
	}
	if filters.ActorID > 0 {
		query = query.Where("actor_id = ?", filters.ActorID)
	}
	if filters.Action != "" {
		query = query.Where("action = ?", filters.Action)
	}
	if filters.TargetType != "" {
		query = query.Where("target_type = ?", filters.TargetType)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := query.Order("timestamp DESC").Offset(offset).Limit(pageSize).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
