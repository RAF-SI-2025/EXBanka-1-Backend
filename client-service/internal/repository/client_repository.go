package repository

import (
	"github.com/exbanka/client-service/internal/model"
	"gorm.io/gorm"
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
	return r.db.Save(client).Error
}

func (r *ClientRepository) SetPassword(userID uint64, hash string) error {
	return r.db.Model(&model.Client{}).Where("id = ?", userID).
		Updates(map[string]interface{}{
			"password_hash": hash,
			"activated":     true,
		}).Error
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
