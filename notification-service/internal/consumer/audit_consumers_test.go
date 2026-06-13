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
	"github.com/exbanka/notification-service/internal/repository"
)

func newConsumerDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// --- AdminAuditConsumer -----------------------------------------------------

func TestAdminAuditConsumer_HandleMessage_WritesRow(t *testing.T) {
	db := newConsumerDB(t, &model.AdminAuditLog{})
	c := &AdminAuditConsumer{db: db}

	ts := time.Now().UTC().Truncate(time.Second)
	payload, _ := json.Marshal(kafkamsg.AdminCronActionMessage{
		Action:     "pause",
		Service:    "credit-service",
		CronName:   "installment-sweep",
		EmployeeID: 11,
		Reason:     "scheduled maintenance",
		Timestamp:  ts,
	})

	if err := c.handleMessage(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}

	var rows []model.AdminAuditLog
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.Action != "pause" || got.Service != "credit-service" || got.CronName != "installment-sweep" ||
		got.EmployeeID != 11 || got.Reason != "scheduled maintenance" {
		t.Fatalf("row mismatch: %+v", got)
	}
}

func TestAdminAuditConsumer_HandleMessage_MalformedIgnored(t *testing.T) {
	db := newConsumerDB(t, &model.AdminAuditLog{})
	c := &AdminAuditConsumer{db: db}

	if err := c.handleMessage(context.Background(), []byte("not json")); err != nil {
		t.Fatalf("malformed should return nil (not retryable), got %v", err)
	}
	var count int64
	db.Model(&model.AdminAuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("malformed message must not write a row, count=%d", count)
	}
}

func TestAdminAuditConsumer_HandleMessage_DBErrorRetried(t *testing.T) {
	// No table migrated → Create fails → handleMessage returns a (retryable) error.
	db := newConsumerDB(t) // nothing migrated
	c := &AdminAuditConsumer{db: db}

	payload, _ := json.Marshal(kafkamsg.AdminCronActionMessage{Action: "resume", Timestamp: time.Now()})
	if err := c.handleMessage(context.Background(), payload); err == nil {
		t.Fatal("expected a transient DB error to be returned for retry")
	}
}

func TestNewAdminAuditConsumer_StartAndClose(t *testing.T) {
	db := newConsumerDB(t, &model.AdminAuditLog{})
	c := NewAdminAuditConsumer("127.0.0.1:1", db, nil, nil)
	if c == nil || c.reader == nil {
		t.Fatal("nil consumer or reader")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(40 * time.Millisecond)
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// --- BusinessAuditConsumer constructor / lifecycle --------------------------

func TestNewBusinessAuditConsumer_StartAndClose(t *testing.T) {
	db := newConsumerDB(t, &model.BusinessAuditLog{})
	c := NewBusinessAuditConsumer("127.0.0.1:1", db, nil, nil)
	if c == nil || c.reader == nil {
		t.Fatal("nil consumer or reader")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(40 * time.Millisecond)
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestBusinessAuditConsumer_HandleMessage_DBErrorRetried(t *testing.T) {
	db := newConsumerDB(t) // no table
	c := &BusinessAuditConsumer{db: db}
	payload, _ := json.Marshal(kafkamsg.BusinessAuditActionMessage{Action: "limit.set", Timestamp: time.Now()})
	if err := c.handleMessage(context.Background(), payload); err == nil {
		t.Fatal("expected a transient DB error to be returned for retry")
	}
}

// --- WatchlistAlertConsumer Start + adapter ---------------------------------

func TestWatchlistAlertConsumer_StartCtxCancel(t *testing.T) {
	c := NewWatchlistAlertConsumer("127.0.0.1:1", nil, nil, nil, nil)
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(40 * time.Millisecond)
}

func TestWatchlistAlertConsumer_Close_NilReader(t *testing.T) {
	// The test constructor leaves reader nil; Close must short-circuit to nil.
	c := newWatchlistAlertConsumerForTest(&stubWatchlistNotifCreator{}, &stubGeneralRenderer{})
	if err := c.Close(); err != nil {
		t.Fatalf("Close with nil reader should return nil, got %v", err)
	}
}

func TestEmailConsumer_PublishConfirmation_NilProducerSafe(t *testing.T) {
	// A test-address message routes through publishConfirmation; with a nil
	// producer the method must short-circuit without panicking.
	c := newEmailConsumerForTest(&stubEmailSender{}, nil, &stubRenderer{subject: "S", body: "B"})
	payload := mustMarshal(t, kafkamsg.SendEmailMessage{
		To:        "user+test@example.com",
		EmailType: kafkamsg.EmailTypeActivation,
		Data:      map[string]string{},
	})
	if err := c.handleMessage(context.Background(), payload); err != nil {
		t.Fatalf("test-address message should be handled without error, got %v", err)
	}
	// No real email is sent for a test address.
	if c.sender.(*stubEmailSender).sentCount() != 0 {
		t.Fatalf("test address must not trigger a real send")
	}
}

func TestWatchlistNotifCreatorAdapter_Delegates(t *testing.T) {
	db := newConsumerDB(t, &model.GeneralNotification{})
	adapter := &watchlistNotifCreatorAdapter{repo: repository.NewGeneralNotificationRepository(db)}

	// Empty key → plain create path (no unique index needed).
	created, err := adapter.CreateWithIdempotency(&model.GeneralNotification{
		UserID: 9, Type: "WATCHLIST_PRICE_MOVE", Title: "t", Message: "m",
	}, "")
	if err != nil {
		t.Fatalf("adapter create: %v", err)
	}
	if !created {
		t.Fatal("expected created=true via empty-key fallback")
	}
	var count int64
	db.Model(&model.GeneralNotification{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 row persisted, got %d", count)
	}
}
