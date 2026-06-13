package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestEmployeeLimitReplica_Migrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&EmployeeLimitReplica{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := EmployeeLimitReplica{EmployeeID: 1, MaxLoanApprovalAmount: decimal.NewFromInt(50000), Version: 1}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got EmployeeLimitReplica
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(50000)) {
		t.Fatalf("bad read: %+v", got)
	}
}
