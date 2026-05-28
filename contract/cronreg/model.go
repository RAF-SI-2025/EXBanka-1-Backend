package cronreg

import "time"

// CronPauseState is the GORM model for the cron_pause_state table.
// One row per cron name; persists pause/resume across service restarts.
type CronPauseState struct {
	Name      string     `gorm:"primaryKey;size:100"`
	IsPaused  bool       `gorm:"not null;default:false"`
	PausedBy  int64
	PausedAt  *time.Time
	UpdatedAt time.Time
}

// TableName overrides the default GORM table name.
func (CronPauseState) TableName() string { return "cron_pause_state" }
