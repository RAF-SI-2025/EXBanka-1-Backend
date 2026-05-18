package service

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/shared/outbox"
)

// newOutboxTestDB returns an in-memory sqlite DB with the outbox.Event
// table migrated. Used by the post-saga publish test below to assert that
// a saga's Kafka publish lands as a pending outbox row instead of (or in
// addition to) hitting the kafka producer directly.
func newOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&outbox.Event{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestOTCOfferService_PublishViaOutboxOrDirect_OutboxPath verifies that
// when the outbox is wired (production path), a post-saga Kafka publish
// lands as a pending outbox row stamped with the saga ID, instead of
// going through the legacy producer.PublishRaw direct-publish.
func TestOTCOfferService_PublishViaOutboxOrDirect_OutboxPath(t *testing.T) {
	db := newOutboxTestDB(t)
	ob := outbox.New(db)

	// Construct a bare service and bolt the outbox onto it. WithOutbox
	// returns a copy with the outbox + db wired.
	svc := &OTCOfferService{}
	svc = svc.WithOutbox(ob, db)

	svc.publishViaOutboxOrDirect(context.Background(), "otc.contract-created", []byte(`{"x":1}`), "saga-outbox-1")

	var rows []outbox.Event
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 outbox row, got %d", len(rows))
	}
	row := rows[0]
	if row.Topic != "otc.contract-created" {
		t.Errorf("topic = %q, want otc.contract-created", row.Topic)
	}
	if row.SagaID == nil || *row.SagaID != "saga-outbox-1" {
		t.Errorf("saga_id = %v, want saga-outbox-1", row.SagaID)
	}
	if row.PublishedAt != nil {
		t.Error("expected published_at NULL (drainer not yet run)")
	}
	if string(row.Payload) != `{"x":1}` {
		t.Errorf("payload = %q, want {\"x\":1}", row.Payload)
	}
}

// TestOTCOfferService_PublishViaOutboxOrDirect_NoOutbox_NoOp verifies that
// when neither the outbox nor a producer is wired (legacy unit-test path),
// the helper is a no-op rather than a panic.
func TestOTCOfferService_PublishViaOutboxOrDirect_NoOutbox_NoOp(t *testing.T) {
	svc := &OTCOfferService{}

	// Should not panic and should write nothing anywhere.
	svc.publishViaOutboxOrDirect(context.Background(), "topic.a", []byte("payload"), "saga-1")
}
