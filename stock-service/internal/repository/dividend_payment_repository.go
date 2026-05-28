package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// DividendPaymentRepository provides CRUD for the dividend_payments table.
type DividendPaymentRepository struct {
	db *gorm.DB
}

func NewDividendPaymentRepository(db *gorm.DB) *DividendPaymentRepository {
	return &DividendPaymentRepository{db: db}
}

// Create inserts a new DividendPayment. Idempotent: returns without error when
// the same (security_id, payment_date) already exists. Callers should check
// dp.ID == 0 after the call and re-fetch the existing row if needed.
func (r *DividendPaymentRepository) Create(dp *model.DividendPayment) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Check if a row for (security_id, payment_date) already exists.
		var existing model.DividendPayment
		err := tx.Where("security_id = ? AND payment_date = ?", dp.SecurityID, dp.PaymentDate).First(&existing).Error
		if err == nil {
			// Already exists — treat as a no-op; caller re-fetches via GetBySecurityAndDate.
			dp.ID = 0 // signal to caller
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(dp).Error
	})
}

// GetByID returns a DividendPayment by primary key.
func (r *DividendPaymentRepository) GetByID(id uint64) (*model.DividendPayment, error) {
	var dp model.DividendPayment
	if err := r.db.First(&dp, id).Error; err != nil {
		return nil, err
	}
	return &dp, nil
}

// GetBySecurityAndDate returns the payment for a (security_id, payment_date) pair.
func (r *DividendPaymentRepository) GetBySecurityAndDate(securityID uint64, date time.Time) (*model.DividendPayment, error) {
	var dp model.DividendPayment
	if err := r.db.Where("security_id = ? AND payment_date = ?", securityID, date).First(&dp).Error; err != nil {
		return nil, err
	}
	return &dp, nil
}

// MarkPaidOut updates status to paid_out and sets paid_out_at.
func (r *DividendPaymentRepository) MarkPaidOut(id uint64, paidAt time.Time) error {
	return r.db.Model(&model.DividendPayment{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     "paid_out",
			"paid_out_at": paidAt,
		}).Error
}

// List returns dividend_payments for a security in reverse-chronological order.
func (r *DividendPaymentRepository) ListBySecurityID(securityID uint64, page, pageSize int) ([]model.DividendPayment, int64, error) {
	var rows []model.DividendPayment
	var total int64
	if err := r.db.Model(&model.DividendPayment{}).Where("security_id = ?", securityID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := r.db.Where("security_id = ?", securityID).
		Order("payment_date DESC").
		Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
