package repository

import (
	"time"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type MobileDeviceRepository struct {
	db *gorm.DB
}

func NewMobileDeviceRepository(db *gorm.DB) *MobileDeviceRepository {
	return &MobileDeviceRepository{db: db}
}

func (r *MobileDeviceRepository) Create(device *model.MobileDevice) error {
	return r.db.Create(device).Error
}

func (r *MobileDeviceRepository) GetByID(id int64) (*model.MobileDevice, error) {
	var device model.MobileDevice
	if err := r.db.First(&device, id).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *MobileDeviceRepository) GetByDeviceID(deviceID string) (*model.MobileDevice, error) {
	var device model.MobileDevice
	if err := r.db.Where("device_id = ?", deviceID).First(&device).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *MobileDeviceRepository) GetActiveByUserID(userID int64) (*model.MobileDevice, error) {
	var device model.MobileDevice
	if err := r.db.Where("user_id = ? AND status = ?", userID, "active").First(&device).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *MobileDeviceRepository) Update(device *model.MobileDevice) error {
	result := r.db.Save(device)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound // optimistic lock conflict
	}
	return nil
}

func (r *MobileDeviceRepository) DeactivateAllForUser(userID int64) error {
	now := time.Now()
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.MobileDevice{}).
		Where("user_id = ? AND status = ?", userID, "active").
		Updates(map[string]interface{}{
			"status":         "deactivated",
			"deactivated_at": now,
		}).Error
}

func (r *MobileDeviceRepository) UpdateLastSeen(deviceID string) error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.MobileDevice{}).
		Where("device_id = ?", deviceID).
		Update("last_seen_at", time.Now()).Error
}

// DeactivateAllForUserInTx deactivates all active devices within an existing transaction.
func (r *MobileDeviceRepository) DeactivateAllForUserInTx(tx *gorm.DB, userID int64) error {
	now := time.Now()
	return tx.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.MobileDevice{}).
		Where("user_id = ? AND status = ?", userID, "active").
		Updates(map[string]interface{}{
			"status":         "deactivated",
			"deactivated_at": now,
		}).Error
}

// CreateInTx creates a device within an existing transaction.
func (r *MobileDeviceRepository) CreateInTx(tx *gorm.DB, device *model.MobileDevice) error {
	return tx.Create(device).Error
}

// DB returns the underlying gorm.DB for transaction use.
func (r *MobileDeviceRepository) DB() *gorm.DB {
	return r.db
}
