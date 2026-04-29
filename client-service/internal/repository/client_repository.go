package repository

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/model"
	shared "github.com/exbanka/contract/shared"
)

type ClientRepository struct {
	db *gorm.DB
}

func NewClientRepository(db *gorm.DB) *ClientRepository {
	return &ClientRepository{db: db}
}

func (r *ClientRepository) Create(client *model.Client) error {
	return r.db.Create(client).Error
}

func (r *ClientRepository) GetByID(id uint64) (*model.Client, error) {
	var client model.Client
	err := r.db.First(&client, id).Error
	return &client, err
}

func (r *ClientRepository) GetByEmail(email string) (*model.Client, error) {
	var client model.Client
	err := r.db.Where("email = ?", email).First(&client).Error
	return &client, err
}

func (r *ClientRepository) Update(client *model.Client) error {
	saveRes := r.db.Save(client)
	if saveRes.Error != nil {
		return saveRes.Error
	}
	if saveRes.RowsAffected == 0 {
		return fmt.Errorf("update client(id=%d): %w", client.ID, shared.ErrOptimisticLock)
	}
	return nil
}

func (r *ClientRepository) List(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error) {
	var clients []model.Client
	var total int64
	query := r.db.Model(&model.Client{})
	if emailFilter != "" {
		query = query.Where("email ILIKE ?", "%"+emailFilter+"%")
	}
	if nameFilter != "" {
		query = query.Where("first_name ILIKE ? OR last_name ILIKE ?", "%"+nameFilter+"%", "%"+nameFilter+"%")
	}
	query.Count(&total)
	offset := (page - 1) * pageSize
	err := query.Order("last_name ASC, first_name ASC").Offset(offset).Limit(pageSize).Find(&clients).Error
	return clients, total, err
}
