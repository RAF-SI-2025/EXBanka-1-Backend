// Package outbox implements the transactional-outbox pattern: services enqueue
// Kafka events into an outbox table inside the same DB transaction as the
// business write, and a background drainer publishes pending rows. This makes
// "DB committed but Kafka was down" failures impossible — the row simply waits
// to be drained.
package outbox

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Event is the outbox row. Each saga step that publishes Kafka events writes
// an Event in the same DB transaction as the business write; a background
// drainer publishes pending rows.
type Event struct {
	ID          uint64     `gorm:"primaryKey"`
	Topic       string     `gorm:"size:255;not null"`
	Payload     []byte     `gorm:"not null"`
	Headers     []byte     `gorm:"type:jsonb"`
	SagaID      *string    `gorm:"size:36;index"`
	CreatedAt   time.Time  `gorm:"not null;autoCreateTime"`
	PublishedAt *time.Time `gorm:"index"`
	LastError   string
	Attempt     int `gorm:"not null;default:0"`
}

// TableName overrides the default plural form so every service shares the
// outbox_events table name regardless of GORM naming strategy.
func (Event) TableName() string { return "outbox_events" }

// Outbox writes pending events.
type Outbox struct{ db *gorm.DB }

// New constructs an Outbox bound to db. The bound db is currently unused by
// Enqueue (which takes its own tx) but kept for future helpers (e.g., admin
// queries).
func New(db *gorm.DB) *Outbox { return &Outbox{db: db} }

// Enqueue inserts a pending event. Pass the active transaction so the row
// commits or rolls back atomically with the saga step's business write.
// An empty sagaID stores NULL, which is acceptable for non-saga publishes.
func (o *Outbox) Enqueue(tx *gorm.DB, topic string, payload []byte, sagaID string) error {
	var sid *string
	if sagaID != "" {
		sid = &sagaID
	}
	return tx.Create(&Event{Topic: topic, Payload: payload, SagaID: sid}).Error
}

// Producer is the minimal Kafka producer interface the drainer uses. Services
// adapt their existing kafka producer to satisfy this interface.
type Producer interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

// Drainer reads pending events and publishes them. Run as a goroutine.
type Drainer struct {
	db   *gorm.DB
	prod Producer
}

// NewDrainer constructs a Drainer; call Run in a goroutine, passing a context
// you cancel on shutdown.
func NewDrainer(db *gorm.DB, prod Producer) *Drainer {
	return &Drainer{db: db, prod: prod}
}

// Run loops until ctx is cancelled. Tick interval 500ms.
func (d *Drainer) Run(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.DrainBatch(ctx, 100)
		}
	}
}

// DrainBatch publishes up to limit pending events. On publish failure, the
// row stays pending (PublishedAt remains NULL) and Attempt is incremented +
// LastError captured for diagnostics. Exposed for tests.
func (d *Drainer) DrainBatch(ctx context.Context, limit int) {
	var rows []Event
	d.db.Where("published_at IS NULL").Order("id").Limit(limit).Find(&rows)
	for _, row := range rows {
		if err := d.prod.Publish(ctx, row.Topic, row.Payload); err != nil {
			d.db.Model(&Event{}).Where("id = ?", row.ID).Updates(map[string]interface{}{
				"last_error": err.Error(),
				"attempt":    row.Attempt + 1,
			})
			continue
		}
		now := time.Now()
		d.db.Model(&Event{}).Where("id = ?", row.ID).Update("published_at", now)
	}
}
