package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type StockRepository struct {
	db *gorm.DB
}

func NewStockRepository(db *gorm.DB) *StockRepository {
	return &StockRepository{db: db}
}

func (r *StockRepository) Create(stock *model.Stock) error {
	return r.db.Create(stock).Error
}

func (r *StockRepository) GetByID(id uint64) (*model.Stock, error) {
	var stock model.Stock
	if err := r.db.Preload("Exchange").First(&stock, id).Error; err != nil {
		return nil, err
	}
	return &stock, nil
}

func (r *StockRepository) GetByTicker(ticker string) (*model.Stock, error) {
	var stock model.Stock
	if err := r.db.Where("ticker = ?", ticker).First(&stock).Error; err != nil {
		return nil, err
	}
	return &stock, nil
}

func (r *StockRepository) Update(stock *model.Stock) error {
	result := r.db.Save(stock)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *StockRepository) UpsertByTicker(stock *model.Stock) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.Stock
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ticker = ?", stock.Ticker).First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(stock).Error
			}
			return err
		}
		existing.Name = stock.Name
		existing.OutstandingShares = stock.OutstandingShares
		existing.DividendYield = stock.DividendYield
		existing.ExchangeID = stock.ExchangeID
		existing.Price = stock.Price
		existing.High = stock.High
		existing.Low = stock.Low
		existing.Change = stock.Change
		existing.Volume = stock.Volume
		existing.LastRefresh = stock.LastRefresh
		return tx.Save(&existing).Error
	})
}

func (r *StockRepository) List(filter StockFilter) ([]model.Stock, int64, error) {
	var stocks []model.Stock
	var total int64

	q := r.db.Model(&model.Stock{}).Joins("JOIN stock_exchanges ON stock_exchanges.id = stocks.exchange_id")

	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("stocks.ticker ILIKE ? OR stocks.name ILIKE ?", like, like)
	}
	if filter.ExchangeAcronym != "" {
		q = q.Where("stock_exchanges.acronym ILIKE ?", filter.ExchangeAcronym+"%")
	}
	if filter.MinPrice != nil {
		q = q.Where("stocks.price >= ?", *filter.MinPrice)
	}
	if filter.MaxPrice != nil {
		q = q.Where("stocks.price <= ?", *filter.MaxPrice)
	}
	if filter.MinVolume != nil {
		q = q.Where("stocks.volume >= ?", *filter.MinVolume)
	}
	if filter.MaxVolume != nil {
		q = q.Where("stocks.volume <= ?", *filter.MaxVolume)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q = applySorting(q, "stocks", filter.SortBy, filter.SortOrder)
	q = applyPagination(q, filter.Page, filter.PageSize)

	if err := q.Preload("Exchange").Find(&stocks).Error; err != nil {
		return nil, 0, err
	}
	return stocks, total, nil
}
