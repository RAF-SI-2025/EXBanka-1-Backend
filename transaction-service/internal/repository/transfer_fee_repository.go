package repository

import (
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

type TransferFeeRepository struct {
	db *gorm.DB
}

func NewTransferFeeRepository(db *gorm.DB) *TransferFeeRepository {
	return &TransferFeeRepository{db: db}
}

func (r *TransferFeeRepository) Create(fee *model.TransferFee) error {
	return r.db.Create(fee).Error
}

func (r *TransferFeeRepository) GetByID(id uint64) (*model.TransferFee, error) {
	var fee model.TransferFee
	if err := r.db.First(&fee, id).Error; err != nil {
		return nil, err
	}
	return &fee, nil
}

func (r *TransferFeeRepository) List() ([]model.TransferFee, error) {
	var fees []model.TransferFee
	if err := r.db.Find(&fees).Error; err != nil {
		return nil, err
	}
	return fees, nil
}

func (r *TransferFeeRepository) Update(fee *model.TransferFee) error {
	return r.db.Save(fee).Error
}

func (r *TransferFeeRepository) Deactivate(id uint64) error {
	return r.db.Model(&model.TransferFee{}).Where("id = ?", id).Update("active", false).Error
}

// GetApplicableFees returns all active fee rules that match the given amount, transaction type, and currency.
func (r *TransferFeeRepository) GetApplicableFees(amount decimal.Decimal, txType, currency string) ([]model.TransferFee, error) {
	var fees []model.TransferFee
	query := r.db.Where("active = ?", true).
		Where("(transaction_type = ? OR transaction_type = ?)", txType, "all").
		Where("(currency_code = ? OR currency_code = ?)", currency, "").
		Where("(min_amount = 0 OR min_amount <= ?)", amount)
	if err := query.Find(&fees).Error; err != nil {
		return nil, err
	}
	return fees, nil
}
