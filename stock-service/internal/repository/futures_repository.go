package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

type FuturesRepository struct {
	db *gorm.DB
}

func NewFuturesRepository(db *gorm.DB) *FuturesRepository {
	return &FuturesRepository{db: db}
}

func (r *FuturesRepository) Create(f *model.FuturesContract) error {
	return r.db.Create(f).Error
}

func (r *FuturesRepository) GetByID(id uint64) (*model.FuturesContract, error) {
	var f model.FuturesContract
	if err := r.db.Preload("Exchange").First(&f, id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *FuturesRepository) GetByTicker(ticker string) (*model.FuturesContract, error) {
	var f model.FuturesContract
	if err := r.db.Where("ticker = ?", ticker).First(&f).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *FuturesRepository) Update(f *model.FuturesContract) error {
	result := r.db.Save(f)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *FuturesRepository) UpsertByTicker(f *model.FuturesContract) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.FuturesContract
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", f.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(f).Error
			}
			return err
		}
		existing.Name = f.Name
		existing.ContractSize = f.ContractSize
		existing.ContractUnit = f.ContractUnit
		existing.SettlementDate = f.SettlementDate
		existing.ExchangeID = f.ExchangeID
		existing.Price = f.Price
		existing.High = f.High
		existing.Low = f.Low
		existing.Change = f.Change
		existing.Volume = f.Volume
		existing.LastRefresh = f.LastRefresh
		return tx.Save(&existing).Error
	})
}

// UpdatePriceByTicker updates only the price column for the futures contract with the given ticker.
func (r *FuturesRepository) UpdatePriceByTicker(ticker string, price decimal.Decimal) error {
	return r.db.Model(&model.FuturesContract{}).Where("ticker = ?", ticker).Update("price", price).Error
}

func (r *FuturesRepository) List(filter FuturesFilter) ([]model.FuturesContract, int64, error) {
	var futures []model.FuturesContract
	var total int64

	q := r.db.Model(&model.FuturesContract{}).
		Joins("JOIN stock_exchanges ON stock_exchanges.id = futures_contracts.exchange_id")

	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("futures_contracts.ticker ILIKE ? OR futures_contracts.name ILIKE ?", like, like)
	}
	if filter.ExchangeAcronym != "" {
		q = q.Where("stock_exchanges.acronym ILIKE ?", filter.ExchangeAcronym+"%")
	}
	if filter.MinPrice != nil {
		q = q.Where("futures_contracts.price >= ?", *filter.MinPrice)
	}
	if filter.MaxPrice != nil {
		q = q.Where("futures_contracts.price <= ?", *filter.MaxPrice)
	}
	if filter.MinVolume != nil {
		q = q.Where("futures_contracts.volume >= ?", *filter.MinVolume)
	}
	if filter.MaxVolume != nil {
		q = q.Where("futures_contracts.volume <= ?", *filter.MaxVolume)
	}
	if filter.SettlementDateFrom != nil {
		q = q.Where("futures_contracts.settlement_date >= ?", *filter.SettlementDateFrom)
	}
	if filter.SettlementDateTo != nil {
		q = q.Where("futures_contracts.settlement_date <= ?", *filter.SettlementDateTo)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applySorting(q, "futures_contracts", filter.SortBy, filter.SortOrder)
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Exchange").Find(&futures).Error; err != nil {
		return nil, 0, err
	}
	return futures, total, nil
}
