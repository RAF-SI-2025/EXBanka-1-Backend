package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestClientReplica_Migrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&ClientReplica{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := ClientReplica{ID: 1, Email: "a@b.com", FirstName: "Ana", LastName: "Anic", JMBG: "1234567890123", Version: 1}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got ClientReplica
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Email != "a@b.com" {
		t.Fatalf("bad read: %+v", got)
	}
}
