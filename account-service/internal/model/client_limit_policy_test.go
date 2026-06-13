package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestClientLimitPolicy_Migrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&ClientLimitPolicy{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	p := ClientLimitPolicy{ClientID: 1, DailyLimit: decimal.NewFromInt(5000), MonthlyLimit: decimal.NewFromInt(20000), Version: 1}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got ClientLimitPolicy
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.DailyLimit.Equal(decimal.NewFromInt(5000)) {
		t.Fatalf("bad DailyLimit read: %+v", got)
	}
	if !got.MonthlyLimit.Equal(decimal.NewFromInt(20000)) {
		t.Fatalf("bad MonthlyLimit read: %+v", got)
	}
}
