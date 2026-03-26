package repository

import (
	"github.com/exbanka/card-service/internal/model"
	"gorm.io/gorm"
)

type CardRequestRepository struct {
	db *gorm.DB
}

func NewCardRequestRepository(db *gorm.DB) *CardRequestRepository {
	return &CardRequestRepository{db: db}
}

func (r *CardRequestRepository) Create(req *model.CardRequest) error {
	return r.db.Create(req).Error
}

func (r *CardRequestRepository) GetByID(id uint64) (*model.CardRequest, error) {
	var req model.CardRequest
	if err := r.db.First(&req, id).Error; err != nil {
		return nil, err
	}
	return &req, nil
}

func (r *CardRequestRepository) List(status string, page, pageSize int) ([]model.CardRequest, int64, error) {
	var requests []model.CardRequest
	var total int64

	q := r.db.Model(&model.CardRequest{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := q.Offset(offset).Limit(pageSize).Order("created_at desc").Find(&requests).Error; err != nil {
		return nil, 0, err
	}
	return requests, total, nil
}

func (r *CardRequestRepository) ListByClient(clientID uint64, page, pageSize int) ([]model.CardRequest, int64, error) {
	var requests []model.CardRequest
	var total int64

	q := r.db.Model(&model.CardRequest{}).Where("client_id = ?", clientID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := q.Offset(offset).Limit(pageSize).Order("created_at desc").Find(&requests).Error; err != nil {
		return nil, 0, err
	}
	return requests, total, nil
}

func (r *CardRequestRepository) UpdateStatus(id uint64, status, reason string, approvedBy uint64) error {
	updates := map[string]interface{}{
		"status":      status,
		"reason":      reason,
		"approved_by": approvedBy,
	}
	return r.db.Model(&model.CardRequest{}).Where("id = ?", id).Updates(updates).Error
}
