package repository

import (
	"github.com/exbanka/card-service/internal/model"
	"gorm.io/gorm"
)

type AuthorizedPersonRepository struct {
	db *gorm.DB
}

func NewAuthorizedPersonRepository(db *gorm.DB) *AuthorizedPersonRepository {
	return &AuthorizedPersonRepository{db: db}
}

func (r *AuthorizedPersonRepository) Create(ap *model.AuthorizedPerson) error {
	return r.db.Create(ap).Error
}

func (r *AuthorizedPersonRepository) GetByID(id uint64) (*model.AuthorizedPerson, error) {
	var ap model.AuthorizedPerson
	if err := r.db.First(&ap, id).Error; err != nil {
		return nil, err
	}
	return &ap, nil
}
