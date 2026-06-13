package repository

import (
	"gorm.io/gorm"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// FundDividendPaymentRepository provides CRUD for the fund_dividend_payments table.
type FundDividendPaymentRepository struct {
	db *gorm.DB
}

func NewFundDividendPaymentRepository(db *gorm.DB) *FundDividendPaymentRepository {
	return &FundDividendPaymentRepository{db: db}
}

// Create inserts a FundDividendPayment row inside the provided transaction.
func (r *FundDividendPaymentRepository) Create(tx *gorm.DB, fdp *model.FundDividendPayment) error {
	return tx.Create(fdp).Error
}

// SumByFundID returns the total dividends received by a fund (all time).
// Used by FundService.Statistics to populate total_dividends_paid_rsd.
func (r *FundDividendPaymentRepository) SumByFundID(fundID uint64) (decimal.Decimal, error) {
	var result struct{ Total decimal.Decimal }
	err := r.db.Model(&model.FundDividendPayment{}).
		Select("COALESCE(SUM(amount_rsd), 0) as total").
		Where("fund_id = ?", fundID).
		Scan(&result).Error
	return result.Total, err
}

// ListByFundID returns all fund_dividend_payments for a fund, sorted most-recent first.
func (r *FundDividendPaymentRepository) ListByFundID(fundID uint64, page, pageSize int) ([]model.FundDividendPayment, int64, error) {
	var rows []model.FundDividendPayment
	var total int64
	q := r.db.Model(&model.FundDividendPayment{}).Where("fund_id = ?", fundID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListByDividendPaymentID returns all fund_dividend_payments for a dividend_payment_id.
func (r *FundDividendPaymentRepository) ListByDividendPaymentID(paymentID uint64) ([]model.FundDividendPayment, error) {
	var rows []model.FundDividendPayment
	if err := r.db.Where("dividend_payment_id = ?", paymentID).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
