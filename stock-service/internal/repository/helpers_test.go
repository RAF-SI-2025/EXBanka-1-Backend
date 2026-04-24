package repository

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestApplyPagination_ClampsAtMaxPageSize_NotTen verifies the historical bug:
// pageSize > 100 used to be silently reset to 10, which quietly dropped the
// tail of every internal sync call (e.g., listing sync for all 20 futures
// contracts). The fix clamps to MaxPageSize (10000) instead, matching what
// seed/sync callers expect.
func TestApplyPagination_ClampsAtMaxPageSize_NotTen(t *testing.T) {
	type row struct {
		ID uint64 `gorm:"primarykey"`
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&row{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed 150 rows — more than the old 10-row clamp but well below the new
	// 10000 cap.
	rows := make([]row, 150)
	for i := range rows {
		rows[i] = row{ID: uint64(i + 1)}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	tests := []struct {
		name     string
		page     int
		pageSize int
		want     int
	}{
		{name: "explicit 10000 returns all", page: 1, pageSize: 10000, want: 150},
		{name: "above cap clamps to MaxPageSize", page: 1, pageSize: 50000, want: 150},
		{name: "zero falls back to default of 10", page: 1, pageSize: 0, want: 10},
		{name: "negative falls back to default of 10", page: 1, pageSize: -1, want: 10},
		{name: "moderate custom size honoured", page: 1, pageSize: 75, want: 75},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out []row
			q := db.Model(&row{})
			q = applyPagination(q, tc.page, tc.pageSize)
			if err := q.Find(&out).Error; err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(out) != tc.want {
				t.Errorf("pageSize=%d: got %d rows, want %d", tc.pageSize, len(out), tc.want)
			}
		})
	}
}
