package repository

import (
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/changelog"
	"gorm.io/gorm"
)

// ChangelogRepository provides append-only access to the changelogs table.
type ChangelogRepository struct {
	db *gorm.DB
}

func NewChangelogRepository(db *gorm.DB) *ChangelogRepository {
	return &ChangelogRepository{db: db}
}

// Create persists one changelog entry.
func (r *ChangelogRepository) Create(entry changelog.Entry) error {
	row := model.Changelog{
		EntityType: entry.EntityType,
		EntityID:   entry.EntityID,
		Action:     entry.Action,
		FieldName:  entry.FieldName,
		OldValue:   entry.OldValue,
		NewValue:   entry.NewValue,
		ChangedBy:  entry.ChangedBy,
		ChangedAt:  entry.ChangedAt,
		Reason:     entry.Reason,
	}
	return r.db.Create(&row).Error
}

// CreateBatch persists multiple changelog entries in a single INSERT.
func (r *ChangelogRepository) CreateBatch(entries []changelog.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	rows := make([]model.Changelog, len(entries))
	for i, e := range entries {
		rows[i] = model.Changelog{
			EntityType: e.EntityType,
			EntityID:   e.EntityID,
			Action:     e.Action,
			FieldName:  e.FieldName,
			OldValue:   e.OldValue,
			NewValue:   e.NewValue,
			ChangedBy:  e.ChangedBy,
			ChangedAt:  e.ChangedAt,
			Reason:     e.Reason,
		}
	}
	return r.db.Create(&rows).Error
}

// ListByEntity returns all changelog entries for a given entity type and ID,
// ordered by changed_at descending (newest first).
func (r *ChangelogRepository) ListByEntity(entityType string, entityID int64, page, pageSize int) ([]model.Changelog, int64, error) {
	var entries []model.Changelog
	var total int64

	query := r.db.Model(&model.Changelog{}).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("changed_at DESC").Offset(offset).Limit(pageSize).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
