package repository

import (
	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/gorm"
)

type LoanRequestRepository struct {
	db *gorm.DB
}

func NewLoanRequestRepository(db *gorm.DB) *LoanRequestRepository {
	return &LoanRequestRepository{db: db}
}

func (r *LoanRequestRepository) Create(req *model.LoanRequest) error {
	return r.db.Create(req).Error
}

func (r *LoanRequestRepository) GetByID(id uint64) (*model.LoanRequest, error) {
	var req model.LoanRequest
	if err := r.db.First(&req, id).Error; err != nil {
		return nil, err
	}
	return &req, nil
}

func (r *LoanRequestRepository) Update(req *model.LoanRequest) error {
	return r.db.Save(req).Error
}

func (r *LoanRequestRepository) List(typeFilter, accountFilter, statusFilter string, page, pageSize int) ([]model.LoanRequest, int64, error) {
	var requests []model.LoanRequest
	var total int64

	query := r.db.Model(&model.LoanRequest{})
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
	if err := query.Offset(offset).Limit(pageSize).Find(&requests).Error; err != nil {
		return nil, 0, err
	}

	return requests, total, nil
}
