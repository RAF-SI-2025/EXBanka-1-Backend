package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OptionRepository struct {
	db *gorm.DB
}

func NewOptionRepository(db *gorm.DB) *OptionRepository {
	return &OptionRepository{db: db}
}

func (r *OptionRepository) Create(o *model.Option) error {
	return r.db.Create(o).Error
}

func (r *OptionRepository) GetByID(id uint64) (*model.Option, error) {
	var o model.Option
	if err := r.db.Preload("Stock").First(&o, id).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OptionRepository) GetByTicker(ticker string) (*model.Option, error) {
	var o model.Option
	if err := r.db.Where("ticker = ?", ticker).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OptionRepository) Update(o *model.Option) error {
	result := r.db.Save(o)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *OptionRepository) UpsertByTicker(o *model.Option) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Option
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", o.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(o).Error
			}
			return err
		}
		existing.Name = o.Name
		existing.OptionType = o.OptionType
		existing.StrikePrice = o.StrikePrice
		existing.ImpliedVolatility = o.ImpliedVolatility
		existing.Premium = o.Premium
		existing.OpenInterest = o.OpenInterest
		existing.SettlementDate = o.SettlementDate
		if saveErr := tx.Save(&existing).Error; saveErr != nil {
			return saveErr
		}
		// Reflect the persisted identity back onto the caller's struct so
		// downstream code (option-listing linking in particular) can reference
		// opt.ID. Without this, upsert callers see opt.ID == 0 for every
		// already-existing row, which silently breaks SetListingID and leaves
		// options orphaned from their listing rows.
		o.ID = existing.ID
		o.ListingID = existing.ListingID
		o.Version = existing.Version
		o.CreatedAt = existing.CreatedAt
		o.UpdatedAt = existing.UpdatedAt
		return nil
	})
}

func (r *OptionRepository) List(filter OptionFilter) ([]model.Option, int64, error) {
	var options []model.Option
	var total int64

	q := r.db.Model(&model.Option{})

	if filter.StockID != nil {
		q = q.Where("stock_id = ?", *filter.StockID)
	}
	if filter.OptionType != "" {
		q = q.Where("option_type = ?", filter.OptionType)
	}
	if filter.SettlementDate != nil {
		q = q.Where("DATE(settlement_date) = DATE(?)", *filter.SettlementDate)
	}
	if filter.MinStrike != nil {
		q = q.Where("strike_price >= ?", *filter.MinStrike)
	}
	if filter.MaxStrike != nil {
		q = q.Where("strike_price <= ?", *filter.MaxStrike)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = q.Order("strike_price ASC, option_type ASC")
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Stock").Find(&options).Error; err != nil {
		return nil, 0, err
	}
	return options, total, nil
}

func (r *OptionRepository) DeleteExpiredBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("settlement_date < ?", cutoff).Delete(&model.Option{})
	return result.RowsAffected, result.Error
}

// SetListingID sets the option.listing_id for a single row. Uses SkipHooks
// because (a) the Option.BeforeUpdate optimistic-lock hook would add a
// version-zero WHERE clause to this plain Updates() call (see CLAUDE.md
// "NEVER use db.Model(&MyModel{}).Updates(map...) on a versioned model"),
// silently updating zero rows, and (b) this is an authoritative one-way
// link attachment invoked from the seed/option-generation path where
// version-race conflicts are not meaningful.
func (r *OptionRepository) SetListingID(optionID, listingID uint64) error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.Option{}).
		Where("id = ?", optionID).
		Update("listing_id", listingID).Error
}
