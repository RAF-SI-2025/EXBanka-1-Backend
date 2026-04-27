package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OptionContractRepository struct{ db *gorm.DB }

func NewOptionContractRepository(db *gorm.DB) *OptionContractRepository {
	return &OptionContractRepository{db: db}
}

func (r *OptionContractRepository) DB() *gorm.DB { return r.db }

func (r *OptionContractRepository) Create(c *model.OptionContract) error {
	return r.db.Create(c).Error
}

func (r *OptionContractRepository) GetByID(id uint64) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &c, err
}

func (r *OptionContractRepository) GetByOfferID(offerID uint64) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.Where("offer_id = ?", offerID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &c, err
}

func (r *OptionContractRepository) Delete(id uint64) error {
	return r.db.Delete(&model.OptionContract{}, id).Error
}

// Save persists a loaded-then-mutated option contract through GORM's Save
// (UPDATE by primary key). The OptionContract.BeforeUpdate hook attaches the
// optimistic-lock WHERE version=? clause and increments Version on the
// caller's struct.
//
// We use Select("*").Save(...) intentionally: bare db.Save in GORM v1.31.1
// falls back to INSERT...ON CONFLICT(id) DO UPDATE when the initial UPDATE
// matches zero rows (finisher_api.go:109-110), which would silently overwrite
// the winner of an optimistic-lock race and hide the conflict. Selecting "*"
// sets the `selectedUpdate` flag in GORM's Save and disables that fallback
// path, so RowsAffected==0 correctly indicates an optimistic-lock conflict.
func (r *OptionContractRepository) Save(c *model.OptionContract) error {
	res := r.db.Select("*").Save(c)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListByOwner returns option contracts where the owner appears as buyer,
// seller, or either. owner_id may be nil for OwnerType=bank.
func (r *OptionContractRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, role string, statuses []string, page, pageSize int) ([]model.OptionContract, int64, error) {
	q := r.db.Model(&model.OptionContract{})
	switch role {
	case "buyer":
		q = scopeOwner(q, "buyer_owner_type", "buyer_owner_id", ownerType, ownerID)
	case "seller":
		q = scopeOwner(q, "seller_owner_type", "seller_owner_id", ownerType, ownerID)
	default:
		// OR over the buyer and seller owner-pair predicates. Inline since
		// scopeOwner is single-pair.
		if ownerID == nil {
			q = q.Where("(buyer_owner_type = ? AND buyer_owner_id IS NULL) OR (seller_owner_type = ? AND seller_owner_id IS NULL)",
				ownerType, ownerType)
		} else {
			q = q.Where("(buyer_owner_type = ? AND buyer_owner_id = ?) OR (seller_owner_type = ? AND seller_owner_id = ?)",
				ownerType, *ownerID, ownerType, *ownerID)
		}
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}
	var out []model.OptionContract
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// ListExpiring returns up to limit ACTIVE contracts past settlement_date.
func (r *OptionContractRepository) ListExpiring(today string, limit int) ([]model.OptionContract, error) {
	var out []model.OptionContract
	err := r.db.Where("status = ? AND settlement_date < ?",
		model.OptionContractStatusActive, today).
		Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}
