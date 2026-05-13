package service

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/shared/outbox"
)

// TestPublishSagaEvent_Outbox enqueues an event when outbox + db are wired.
func TestPublishSagaEvent_Outbox(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&outbox.Event{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ob := outbox.New(db)
	publishSagaEvent(context.Background(), ob, db, nil, "topic.x", []byte(`{"a":1}`), "saga-z")
	var rows []outbox.Event
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Topic != "topic.x" {
		t.Errorf("topic=%q", rows[0].Topic)
	}
	if rows[0].SagaID == nil || *rows[0].SagaID != "saga-z" {
		t.Errorf("saga_id=%v", rows[0].SagaID)
	}
}

// TestPublishSagaEvent_Direct_NoOp ensures the legacy path doesn't crash
// when neither outbox nor producer is wired (unit-test fallback).
func TestPublishSagaEvent_Direct_NoOp(t *testing.T) {
	publishSagaEvent(context.Background(), nil, nil, nil, "topic.x", []byte("p"), "s")
	// no panic, no assertion needed
}
