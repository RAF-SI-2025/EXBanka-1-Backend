package repository

import (
	"fmt"
	"math/rand"

	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/gorm"
)

type LoanRepository struct {
	db *gorm.DB
}

func NewLoanRepository(db *gorm.DB) *LoanRepository {
	return &LoanRepository{db: db}
}

func (r *LoanRepository) GenerateLoanNumber() string {
	digits := make([]byte, 10)
	for i := range digits {
		digits[i] = byte('0' + rand.Intn(10))
	}
	return fmt.Sprintf("LN%s", string(digits))
}

func (r *LoanRepository) Create(loan *model.Loan) error {
	return r.db.Create(loan).Error
}

func (r *LoanRepository) GetByID(id uint64) (*model.Loan, error) {
	var loan model.Loan
	if err := r.db.First(&loan, id).Error; err != nil {
		return nil, err
	}
	return &loan, nil
}

func (r *LoanRepository) List(clientID uint64, page, pageSize int) ([]model.Loan, int64, error) {
	var loans []model.Loan
	var total int64

	query := r.db.Model(&model.Loan{}).Where("client_id = ?", clientID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if err := query.Offset(offset).Limit(pageSize).Find(&loans).Error; err != nil {
		return nil, 0, err
	}

	return loans, total, nil
}

func (r *LoanRepository) ListAll(typeFilter, accountFilter, statusFilter string, page, pageSize int) ([]model.Loan, int64, error) {
	var loans []model.Loan
	var total int64

	query := r.db.Model(&model.Loan{})
	if typeFilter != "" {
		query = query.Where("loan_type = ?", typeFilter)
	}
	if accountFilter != "" {
		query = query.Where("account_number = ?", accountFilter)
	}
	if statusFilter != "" {
		query = query.Where("status = ?", statusFilter)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if err := query.Offset(offset).Limit(pageSize).Find(&loans).Error; err != nil {
		return nil, 0, err
	}

	return loans, total, nil
}
