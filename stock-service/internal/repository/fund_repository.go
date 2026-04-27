package repository

import (
	"strings"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
)

// ErrFundNameInUse signals an attempt to create or rename a fund to a name
// already used by another active fund (case-insensitive). Typed sentinel
// carrying codes.AlreadyExists.
var ErrFundNameInUse = svcerr.New(codes.AlreadyExists, "fund name already in use")

type FundRepository struct {
	db *gorm.DB
}

func NewFundRepository(db *gorm.DB) *FundRepository {
	return &FundRepository{db: db}
}

func (r *FundRepository) Create(f *model.InvestmentFund) error {
	if err := r.assertNameAvailable(f.Name, 0); err != nil {
		return err
	}
	return r.db.Create(f).Error
}

func (r *FundRepository) GetByID(id uint64) (*model.InvestmentFund, error) {
	var f model.InvestmentFund
	if err := r.db.First(&f, id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *FundRepository) List(search string, active *bool, page, pageSize int) ([]model.InvestmentFund, int64, error) {
	q := r.db.Model(&model.InvestmentFund{})
	if search != "" {
		q = q.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(search)+"%")
	}
	if active != nil {
		q = q.Where("active = ?", *active)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}
	var out []model.InvestmentFund
	err := q.Order("name ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

func (r *FundRepository) Save(f *model.InvestmentFund) error {
	if err := r.assertNameAvailable(f.Name, f.ID); err != nil {
		return err
	}
	res := r.db.Save(f)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ReassignManager moves every active fund managed by `from` (typically a
// demoted supervisor) to `to` (the admin) inside a single TX. Returns the
// fund IDs that were updated so the caller can publish a downstream event.
// Idempotent: a second call with no matching rows returns ([]).
func (r *FundRepository) ReassignManager(from, to int64) ([]uint64, error) {
	var ids []uint64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var rows []model.InvestmentFund
		if err := tx.Where("manager_employee_id = ?", from).Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].ManagerEmployeeID = to
			if err := tx.Save(&rows[i]).Error; err != nil {
				return err
			}
			ids = append(ids, rows[i].ID)
		}
		return nil
	})
	return ids, err
}

func (r *FundRepository) assertNameAvailable(name string, ignoreID uint64) error {
	var count int64
	q := r.db.Model(&model.InvestmentFund{}).
		Where("LOWER(name) = ? AND active = ?", strings.ToLower(name), true)
	if ignoreID != 0 {
		q = q.Where("id <> ?", ignoreID)
	}
	if err := q.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrFundNameInUse
	}
	return nil
}
