package model

// SystemSetting stores global key-value configuration.
type SystemSetting struct {
	Key   string `gorm:"primaryKey;size:64" json:"key"`
	Value string `gorm:"not null" json:"value"`
}
