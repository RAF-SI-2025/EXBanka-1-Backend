package repository

import (
	"github.com/exbanka/user-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ActuaryRepository struct {
	db *gorm.DB
}

func NewActuaryRepository(db *gorm.DB) *ActuaryRepository {
	return &ActuaryRepository{db: db}
}

func (r *ActuaryRepository) Create(limit *model.ActuaryLimit) error {
	return r.db.Create(limit).Error
}

func (r *ActuaryRepository) GetByID(id int64) (*model.ActuaryLimit, error) {
	var limit model.ActuaryLimit
	if err := r.db.First(&limit, id).Error; err != nil {
		return nil, err
	}
	return &limit, nil
}

func (r *ActuaryRepository) GetByEmployeeID(employeeID int64) (*model.ActuaryLimit, error) {
	var limit model.ActuaryLimit
	if err := r.db.Where("employee_id = ?", employeeID).First(&limit).Error; err != nil {
		return nil, err
	}
	return &limit, nil
}

func (r *ActuaryRepository) Upsert(limit *model.ActuaryLimit) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "employee_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"limit", "need_approval", "updated_at"}),
	}).Create(limit).Error
}

func (r *ActuaryRepository) Save(limit *model.ActuaryLimit) error {
	result := r.db.Save(limit)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound // optimistic lock conflict
	}
	return nil
}

// ListActuaries returns employees who have EmployeeAgent or EmployeeSupervisor
// role, along with their actuary limits. Filters by name/email/position.
func (r *ActuaryRepository) ListActuaries(search, position string, page, pageSize int) ([]model.ActuaryRow, int64, error) {
	var rows []model.ActuaryRow
	var total int64

	q := r.db.Table("employees").
		Select(`employees.id as employee_id, employees.first_name, employees.last_name,
			employees.email, employees.position,
			COALESCE(actuary_limits."limit", 0) as "limit",
			COALESCE(actuary_limits.used_limit, 0) as used_limit,
			COALESCE(actuary_limits.need_approval, false) as need_approval,
			actuary_limits.id as actuary_limit_id`).
		Joins("INNER JOIN employee_roles ON employee_roles.employee_id = employees.id").
		Joins("INNER JOIN roles ON roles.id = employee_roles.role_id").
		Joins("LEFT JOIN actuary_limits ON actuary_limits.employee_id = employees.id").
		Where("roles.name IN ?", []string{"EmployeeAgent", "EmployeeSupervisor"}).
		Group("employees.id, actuary_limits.id")

	if search != "" {
		like := "%" + search + "%"
		q = q.Where("employees.email ILIKE ? OR employees.first_name ILIKE ? OR employees.last_name ILIKE ?", like, like, like)
	}
	if position != "" {
		q = q.Where("employees.position ILIKE ?", "%"+position+"%")
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).
		Order("employees.last_name ASC").Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ResetAllUsedLimits resets used_limit to 0 for all actuary limits.
// Used by the daily cron job. Skips optimistic locking.
func (r *ActuaryRepository) ResetAllUsedLimits() error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.ActuaryLimit{}).
		Where("used_limit > 0").
		Update("used_limit", 0).Error
}
