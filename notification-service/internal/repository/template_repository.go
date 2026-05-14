package repository

import (
	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TemplateRepository struct {
	db *gorm.DB
}

func NewTemplateRepository(db *gorm.DB) *TemplateRepository {
	return &TemplateRepository{db: db}
}

// Get returns the override row for (type, channel), or gorm.ErrRecordNotFound
// when the admin has not customized that template.
func (r *TemplateRepository) Get(typ, channel string) (*model.NotificationTemplate, error) {
	var t model.NotificationTemplate
	if err := r.db.Where("type = ? AND channel = ?", typ, channel).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// Upsert inserts or updates the override row for (type, channel). Uses
// ON CONFLICT on the (type, channel) unique index so concurrent admin edits
// do not race into duplicate rows.
func (r *TemplateRepository) Upsert(t *model.NotificationTemplate) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "channel"}},
		DoUpdates: clause.AssignmentColumns([]string{"subject", "body", "updated_by", "updated_at"}),
	}).Create(t).Error
}

// Delete removes the override row, reverting (type, channel) to the registry
// default. Deleting a non-existent row is a no-op (not an error).
func (r *TemplateRepository) Delete(typ, channel string) error {
	return r.db.Where("type = ? AND channel = ?", typ, channel).
		Delete(&model.NotificationTemplate{}).Error
}
