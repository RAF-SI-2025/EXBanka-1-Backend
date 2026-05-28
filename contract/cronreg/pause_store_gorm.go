package cronreg

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// GormPauseStore implements PauseStore using a GORM DB connection.
// The underlying table is cron_pause_state (see model.go).
type GormPauseStore struct {
	db *gorm.DB
}

// NewGormPauseStore creates a GormPauseStore backed by db.
// The caller is responsible for running db.AutoMigrate(&CronPauseState{}) before use.
func NewGormPauseStore(db *gorm.DB) *GormPauseStore { return &GormPauseStore{db: db} }

// Load returns the persisted pause state for cronName, or (false, 0, zero, nil)
// if no row exists.
func (s *GormPauseStore) Load(cronName string) (bool, int64, time.Time, error) {
	var row CronPauseState
	if err := s.db.Where("name = ?", cronName).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, 0, time.Time{}, nil
		}
		return false, 0, time.Time{}, err
	}
	at := time.Time{}
	if row.PausedAt != nil {
		at = *row.PausedAt
	}
	return row.IsPaused, row.PausedBy, at, nil
}

// Save upserts the pause state for cronName. When paused is false, PausedAt is
// set to nil to clear the timestamp.
func (s *GormPauseStore) Save(cronName string, paused bool, by int64, at time.Time) error {
	pa := &at
	if !paused {
		pa = nil
	}
	row := CronPauseState{Name: cronName, IsPaused: paused, PausedBy: by, PausedAt: pa}
	return s.db.Save(&row).Error
}
