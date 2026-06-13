package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
)

func TestBusinessAuditConsumer_HandleMessage_WritesRow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.BusinessAuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	c := &BusinessAuditConsumer{db: db}

	ts := time.Now().UTC().Truncate(time.Second)
	payload, _ := json.Marshal(kafkamsg.BusinessAuditActionMessage{
		Action:          "limit.set",
		ActorEmployeeID: 5,
		TargetType:      "employee",
		TargetID:        "7",
		Detail:          "max_single_transaction=5000",
		Timestamp:       ts,
	})

	_ = c.handleMessage(context.Background(), payload)

	var rows []model.BusinessAuditLog
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.Action != "limit.set" || got.ActorID != 5 || got.TargetType != "employee" || got.TargetID != "7" || got.Detail != "max_single_transaction=5000" {
		t.Fatalf("row mismatch: %+v", got)
	}

	// Malformed JSON is ignored (no panic, no row).
	_ = c.handleMessage(context.Background(), []byte("not json"))
	var count int64
	db.Model(&model.BusinessAuditLog{}).Count(&count)
	if count != 1 {
		t.Fatalf("malformed message must not write a row, count=%d", count)
	}
}
