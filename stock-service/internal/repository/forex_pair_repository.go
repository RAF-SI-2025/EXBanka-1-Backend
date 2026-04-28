package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

type ForexPairRepository struct {
	db *gorm.DB
}

func NewForexPairRepository(db *gorm.DB) *ForexPairRepository {
	return &ForexPairRepository{db: db}
}

func (r *ForexPairRepository) Create(fp *model.ForexPair) error {
	return r.db.Create(fp).Error
}

func (r *ForexPairRepository) GetByID(id uint64) (*model.ForexPair, error) {
	var fp model.ForexPair
	if err := r.db.Preload("Exchange").First(&fp, id).Error; err != nil {
		return nil, err
	}
	return &fp, nil
}

func (r *ForexPairRepository) GetByTicker(ticker string) (*model.ForexPair, error) {
	var fp model.ForexPair
	if err := r.db.Where("ticker = ?", ticker).First(&fp).Error; err != nil {
		return nil, err
	}
	return &fp, nil
}

func (r *ForexPairRepository) Update(fp *model.ForexPair) error {
	result := r.db.Save(fp)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *ForexPairRepository) UpsertByTicker(fp *model.ForexPair) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.ForexPair
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", fp.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(fp).Error
			}
			return err
		}
		existing.Name = fp.Name
		existing.BaseCurrency = fp.BaseCurrency
		existing.QuoteCurrency = fp.QuoteCurrency
		existing.ExchangeRate = fp.ExchangeRate
		existing.Liquidity = fp.Liquidity
		existing.ExchangeID = fp.ExchangeID
		existing.High = fp.High
		existing.Low = fp.Low
		existing.Change = fp.Change
		existing.Volume = fp.Volume
		existing.LastRefresh = fp.LastRefresh
		return CheckRowsAffected(tx.Save(&existing))
	})
}

// UpdatePriceByTicker updates only the exchange_rate column for the forex pair with the given ticker.
// Uses SkipHooks because Model(&ForexPair{}) creates a zero-value struct; the BeforeUpdate
// optimistic-lock hook would add WHERE version=0 and match nothing (see CLAUDE.md §3a).
func (r *ForexPairRepository) UpdatePriceByTicker(ticker string, rate decimal.Decimal) error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.ForexPair{}).Where("ticker = ?", ticker).Update("exchange_rate", rate).Error
}

func (r *ForexPairRepository) List(filter ForexFilter) ([]model.ForexPair, int64, error) {
	var pairs []model.ForexPair
	var total int64

	q := r.db.Model(&model.ForexPair{})

	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("ticker ILIKE ? OR name ILIKE ?", like, like)
	}
	if filter.BaseCurrency != "" {
		q = q.Where("base_currency = ?", filter.BaseCurrency)
	}
	if filter.QuoteCurrency != "" {
		q = q.Where("quote_currency = ?", filter.QuoteCurrency)
	}
	if filter.Liquidity != "" {
		q = q.Where("liquidity = ?", filter.Liquidity)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	sortCol := "exchange_rate"
	if filter.SortBy == "volume" {
		sortCol = "volume"
	} else if filter.SortBy == "change" {
		sortCol = "change"
	}
	dir := "ASC"
	if filter.SortOrder == "desc" {
		dir = "DESC"
	}
	q = q.Order(sortCol + " " + dir)
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Exchange").Find(&pairs).Error; err != nil {
		return nil, 0, err
	}
	return pairs, total, nil
}
