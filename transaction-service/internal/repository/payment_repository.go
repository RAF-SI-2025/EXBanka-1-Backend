package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

type PaymentRepository struct {
	db *gorm.DB
}

func NewPaymentRepository(db *gorm.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

func (r *PaymentRepository) Create(payment *model.Payment) error {
	return r.db.Create(payment).Error
}

func (r *PaymentRepository) GetByID(id uint64) (*model.Payment, error) {
	var payment model.Payment
	if err := r.db.First(&payment, id).Error; err != nil {
		return nil, err
	}
	return &payment, nil
}

func (r *PaymentRepository) ListByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error) {
	q := r.db.Model(&model.Payment{}).Where(
		"from_account_number = ? OR to_account_number = ?", accountNumber, accountNumber,
	)

	if dateFrom != "" {
		if t, err := time.Parse(time.RFC3339, dateFrom); err == nil {
			q = q.Where("timestamp >= ?", t)
		}
	}
	if dateTo != "" {
		if t, err := time.Parse(time.RFC3339, dateTo); err == nil {
			q = q.Where("timestamp <= ?", t)
		}
	}
	if statusFilter != "" {
		q = q.Where("status = ?", statusFilter)
	}
	if amountMin > 0 {
		q = q.Where("initial_amount >= ?", amountMin)
	}
	if amountMax > 0 {
		q = q.Where("initial_amount <= ?", amountMax)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var payments []model.Payment
	if err := q.Order("timestamp DESC").Offset(offset).Limit(pageSize).Find(&payments).Error; err != nil {
		return nil, 0, err
	}
	return payments, total, nil
}

func (r *PaymentRepository) UpdateStatus(id uint64, status string) error {
	return r.db.Model(&model.Payment{}).Where("id = ?", id).Update("status", status).Error
}
