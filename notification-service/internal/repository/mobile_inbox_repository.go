package repository

import (
	"time"

	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
)

type MobileInboxRepository struct {
	db *gorm.DB
}

func NewMobileInboxRepository(db *gorm.DB) *MobileInboxRepository {
	return &MobileInboxRepository{db: db}
}

func (r *MobileInboxRepository) Create(item *model.MobileInboxItem) error {
	return r.db.Create(item).Error
}

// GetPendingByUser returns all pending, non-expired inbox items for a user.
// Browser-initiated and mobile-initiated challenges are returned equally.
func (r *MobileInboxRepository) GetPendingByUser(userID uint64) ([]model.MobileInboxItem, error) {
	var items []model.MobileInboxItem
	if err := r.db.Where("user_id = ? AND status = ? AND expires_at > ?",
		userID, "pending", time.Now()).
		Order("created_at DESC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *MobileInboxRepository) MarkDelivered(id uint64) error {
	now := time.Now()
	result := r.db.Model(&model.MobileInboxItem{}).
		Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]interface{}{
			"status":       "delivered",
			"delivered_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MobileInboxRepository) DeleteExpired() (int64, error) {
	result := r.db.Where("expires_at < ?", time.Now()).Delete(&model.MobileInboxItem{})
	return result.RowsAffected, result.Error
}
