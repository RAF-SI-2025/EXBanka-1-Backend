package repository

import (
	"time"

	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
)

// AdminAuditLogRepository provides read access to the admin_audit_logs table.
type AdminAuditLogRepository struct {
	db *gorm.DB
}

func NewAdminAuditLogRepository(db *gorm.DB) *AdminAuditLogRepository {
	return &AdminAuditLogRepository{db: db}
}

// AdminAuditLogFilters holds optional filters for ListAll.
type AdminAuditLogFilters struct {
	Since   int64  // unix seconds, 0 = no lower bound
	Until   int64  // unix seconds, 0 = no upper bound
	ActorID int64  // employee_id, 0 = all
	Action  string // exact match, "" = all
}

// ListAll returns paginated admin audit log rows ordered by timestamp DESC.
// Filters are all optional.
func (r *AdminAuditLogRepository) ListAll(filters AdminAuditLogFilters, page, pageSize int) ([]model.AdminAuditLog, int64, error) {
	var entries []model.AdminAuditLog
	var total int64

	query := r.db.Model(&model.AdminAuditLog{})
	if filters.Since > 0 {
		query = query.Where("timestamp >= ?", time.Unix(filters.Since, 0))
	}
	if filters.Until > 0 {
		query = query.Where("timestamp <= ?", time.Unix(filters.Until, 0))
	}
	if filters.ActorID > 0 {
		query = query.Where("employee_id = ?", filters.ActorID)
	}
	if filters.Action != "" {
		query = query.Where("action = ?", filters.Action)
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
