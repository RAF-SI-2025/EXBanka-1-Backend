package repository

import (
	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
)

type GeneralNotificationRepository struct {
	db *gorm.DB
}

func NewGeneralNotificationRepository(db *gorm.DB) *GeneralNotificationRepository {
	return &GeneralNotificationRepository{db: db}
}

func (r *GeneralNotificationRepository) Create(n *model.GeneralNotification) error {
	return r.db.Create(n).Error
}

// ListByUser returns paginated notifications for a user.
// readFilter: nil = all, ptr to true = read only, ptr to false = unread only.
func (r *GeneralNotificationRepository) ListByUser(userID uint64, readFilter *bool, page, pageSize int) ([]model.GeneralNotification, int64, error) {
	q := r.db.Model(&model.GeneralNotification{}).Where("user_id = ?", userID)
	if readFilter != nil {
		q = q.Where("is_read = ?", *readFilter)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.GeneralNotification
	offset := (page - 1) * pageSize
	if err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *GeneralNotificationRepository) UnreadCount(userID uint64) (int64, error) {
	var count int64
	err := r.db.Model(&model.GeneralNotification{}).
		Where("user_id = ? AND is_read = false", userID).
		Count(&count).Error
	return count, err
}

func (r *GeneralNotificationRepository) MarkRead(id, userID uint64) error {
	result := r.db.Model(&model.GeneralNotification{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("is_read", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *GeneralNotificationRepository) MarkAllRead(userID uint64) (int64, error) {
	result := r.db.Model(&model.GeneralNotification{}).
		Where("user_id = ? AND is_read = false", userID).
		Update("is_read", true)
	return result.RowsAffected, result.Error
}
