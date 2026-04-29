package repository

import (
	"github.com/exbanka/transaction-service/internal/model"
	"gorm.io/gorm"
)

// PeerBankRepository is CRUD for the peer_banks table.
type PeerBankRepository struct {
	db *gorm.DB
}

func NewPeerBankRepository(db *gorm.DB) *PeerBankRepository {
	return &PeerBankRepository{db: db}
}

func (r *PeerBankRepository) Create(pb *model.PeerBank) error {
	return r.db.Create(pb).Error
}

func (r *PeerBankRepository) GetByID(id uint64) (*model.PeerBank, error) {
	var pb model.PeerBank
	if err := r.db.First(&pb, id).Error; err != nil {
		return nil, err
	}
	return &pb, nil
}

func (r *PeerBankRepository) GetByBankCode(code string) (*model.PeerBank, error) {
	var pb model.PeerBank
	if err := r.db.Where("bank_code = ?", code).First(&pb).Error; err != nil {
		return nil, err
	}
	return &pb, nil
}

func (r *PeerBankRepository) List(activeOnly bool) ([]model.PeerBank, error) {
	var pbs []model.PeerBank
	q := r.db.Model(&model.PeerBank{})
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	if err := q.Order("bank_code ASC").Find(&pbs).Error; err != nil {
		return nil, err
	}
	return pbs, nil
}

func (r *PeerBankRepository) Update(pb *model.PeerBank) error {
	return r.db.Save(pb).Error
}

func (r *PeerBankRepository) Delete(id uint64) error {
	return r.db.Delete(&model.PeerBank{}, id).Error
}
