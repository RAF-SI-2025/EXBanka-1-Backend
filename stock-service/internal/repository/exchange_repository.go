package repository

import (
	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
)

type ExchangeRepository struct {
	db *gorm.DB
}

func NewExchangeRepository(db *gorm.DB) *ExchangeRepository {
	return &ExchangeRepository{db: db}
}

func (r *ExchangeRepository) Create(exchange *model.StockExchange) error {
	return r.db.Create(exchange).Error
}

func (r *ExchangeRepository) GetByID(id uint64) (*model.StockExchange, error) {
	var exchange model.StockExchange
	if err := r.db.First(&exchange, id).Error; err != nil {
		return nil, err
	}
	return &exchange, nil
}

func (r *ExchangeRepository) GetByMICCode(mic string) (*model.StockExchange, error) {
	var exchange model.StockExchange
	if err := r.db.Where("mic_code = ?", mic).First(&exchange).Error; err != nil {
		return nil, err
	}
	return &exchange, nil
}

func (r *ExchangeRepository) GetByAcronym(acronym string) (*model.StockExchange, error) {
	var exchange model.StockExchange
	if err := r.db.Where("acronym = ?", acronym).First(&exchange).Error; err != nil {
		return nil, err
	}
	return &exchange, nil
}

func (r *ExchangeRepository) List(search string, page, pageSize int) ([]model.StockExchange, int64, error) {
	var exchanges []model.StockExchange
	var total int64

	q := r.db.Model(&model.StockExchange{})
	if search != "" {
		like := "%" + search + "%"
		q = q.Where("name ILIKE ? OR acronym ILIKE ? OR mic_code ILIKE ?", like, like, like)
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
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Order("name ASC").Find(&exchanges).Error; err != nil {
		return nil, 0, err
	}
	return exchanges, total, nil
}

func (r *ExchangeRepository) UpsertByMICCode(exchange *model.StockExchange) error {
	existing, err := r.GetByMICCode(exchange.MICCode)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return r.db.Create(exchange).Error
		}
		return err
	}
	existing.Name = exchange.Name
	existing.Acronym = exchange.Acronym
	existing.Polity = exchange.Polity
	existing.Currency = exchange.Currency
	existing.TimeZone = exchange.TimeZone
	existing.OpenTime = exchange.OpenTime
	existing.CloseTime = exchange.CloseTime
	existing.PreMarketOpen = exchange.PreMarketOpen
	existing.PostMarketClose = exchange.PostMarketClose
	return r.db.Save(existing).Error
}
