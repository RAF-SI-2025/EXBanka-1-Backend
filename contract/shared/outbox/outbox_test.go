package outbox_test

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/contract/shared/outbox"
)

// newTestDB returns an in-memory sqlite gorm.DB with the Event table migrated.
// We override the jsonb column type for sqlite (it understands BLOB) by
// migrating with default mappings; gorm coerces the jsonb tag to BLOB on sqlite.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestEnqueue_WritesPendingRow(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&outbox.Event{}); err != nil {
		t.Fatal(err)
	}

	ob := outbox.New(db)
	tx := db.Begin()
	if err := ob.Enqueue(tx, "test.topic", []byte("payload"), "saga-1"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}

	var rows []outbox.Event
	db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Topic != "test.topic" || rows[0].PublishedAt != nil {
		t.Errorf("row: %+v", rows[0])
	}
	if rows[0].SagaID == nil || *rows[0].SagaID != "saga-1" {
		t.Errorf("saga id not stored: %+v", rows[0].SagaID)
	}
}

func TestEnqueue_RolledBackWhenTxRollsBack(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&outbox.Event{}); err != nil {
		t.Fatal(err)
	}

	ob := outbox.New(db)
	tx := db.Begin()
	_ = ob.Enqueue(tx, "test.topic", []byte("p"), "s")
	_ = tx.Rollback()

	var count int64
	db.Model(&outbox.Event{}).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %d", count)
	}
}

type fakeProducer struct {
	calls    []string
	failNext bool
}

func (p *fakeProducer) Publish(_ context.Context, topic string, _ []byte) error {
	if p.failNext {
		p.failNext = false
		return errors.New("kafka down")
	}
	p.calls = append(p.calls, topic)
	return nil
}

func TestDrainer_PublishesPendingThenMarks(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&outbox.Event{}); err != nil {
		t.Fatal(err)
	}
	ob := outbox.New(db)
	if err := ob.Enqueue(db, "topic-A", []byte("a"), "s1"); err != nil {
		t.Fatal(err)
	}
	if err := ob.Enqueue(db, "topic-B", []byte("b"), "s2"); err != nil {
		t.Fatal(err)
	}

	prod := &fakeProducer{}
	d := outbox.NewDrainer(db, prod)
	d.DrainBatch(context.Background(), 100)

	if len(prod.calls) != 2 {
		t.Errorf("expected 2 publishes, got %d", len(prod.calls))
	}
	var pending int64
	db.Model(&outbox.Event{}).Where("published_at IS NULL").Count(&pending)
	if pending != 0 {
		t.Errorf("expected 0 pending after drain, got %d", pending)
	}
}

func TestDrainer_KafkaFailureLeavesRowPendingAndIncrementsAttempt(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&outbox.Event{}); err != nil {
		t.Fatal(err)
	}
	ob := outbox.New(db)
	if err := ob.Enqueue(db, "topic-X", []byte("x"), "s1"); err != nil {
		t.Fatal(err)
	}

	prod := &fakeProducer{failNext: true}
	d := outbox.NewDrainer(db, prod)
	d.DrainBatch(context.Background(), 100)

	var row outbox.Event
	db.First(&row)
	if row.PublishedAt != nil {
		t.Error("expected still pending")
	}
	if row.Attempt != 1 {
		t.Errorf("expected attempt=1, got %d", row.Attempt)
	}
	if row.LastError == "" {
		t.Error("expected last_error to be populated")
	}

	// Second drain succeeds.
	d.DrainBatch(context.Background(), 100)
	db.First(&row)
	if row.PublishedAt == nil {
		t.Error("expected published after retry")
	}
}
