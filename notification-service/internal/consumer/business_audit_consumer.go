package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
	kafkago "github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

// BusinessAuditConsumer subscribes to the admin.business-action Kafka topic and
// persists each event as a BusinessAuditLog row. It is the authoritative audit
// trail for high-value business actions (limit changes, usedLimit resets, order
// approve/reject, permission changes, manual tax collection).
type BusinessAuditConsumer struct {
	reader *kafkago.Reader
	db     *gorm.DB
	dlq    DeadLetterWriter
	dedup  Deduper
}

// NewBusinessAuditConsumer constructs a BusinessAuditConsumer and configures the
// Kafka reader. The caller must call Start(ctx) to begin consuming.
func NewBusinessAuditConsumer(brokers string, db *gorm.DB, dlq DeadLetterWriter, dedup Deduper) *BusinessAuditConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicBusinessAuditAction,
		GroupID:  "notification-service-business-audit",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &BusinessAuditConsumer{reader: reader, db: db, dlq: dlq, dedup: dedup}
}

// Start launches the consumer loop in a goroutine (manual-commit + retry + DLQ).
func (c *BusinessAuditConsumer) Start(ctx context.Context) {
	go runConsumer(ctx, "business_audit", c.reader, c.dlq, c.dedup, c.handleMessage)
}

func (c *BusinessAuditConsumer) handleMessage(_ context.Context, data []byte) error {
	var event kafkamsg.BusinessAuditActionMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("business audit consumer: dropping malformed message: %v", err)
		return nil // not retryable
	}

	row := &model.BusinessAuditLog{
		Action:     event.Action,
		ActorID:    event.ActorEmployeeID,
		TargetType: event.TargetType,
		TargetID:   event.TargetID,
		Detail:     event.Detail,
		Timestamp:  event.Timestamp,
	}
	if err := c.db.Create(row).Error; err != nil {
		// Transient DB failure — retry then dead-letter. Audit records must
		// never be silently dropped.
		return err
	}
	return nil
}

// Close releases the Kafka reader.
func (c *BusinessAuditConsumer) Close() error {
	return c.reader.Close()
}
