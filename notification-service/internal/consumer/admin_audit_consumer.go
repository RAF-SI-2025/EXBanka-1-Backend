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

// AdminAuditConsumer subscribes to the admin.cron-action Kafka topic and
// persists each event as an AdminAuditLog row. It is the authoritative
// audit trail for admin cron control actions (trigger, pause, resume).
type AdminAuditConsumer struct {
	reader *kafkago.Reader
	db     *gorm.DB
	dlq    DeadLetterWriter
	dedup  Deduper
}

// NewAdminAuditConsumer constructs an AdminAuditConsumer and configures the
// Kafka reader. The caller must call Start(ctx) to begin consuming.
func NewAdminAuditConsumer(brokers string, db *gorm.DB, dlq DeadLetterWriter, dedup Deduper) *AdminAuditConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicAdminCronAction,
		GroupID:  "notification-service-admin-audit",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &AdminAuditConsumer{reader: reader, db: db, dlq: dlq, dedup: dedup}
}

// Start launches the consumer loop in a goroutine (manual-commit + retry + DLQ).
func (c *AdminAuditConsumer) Start(ctx context.Context) {
	go runConsumer(ctx, "admin_audit", c.reader, c.dlq, c.dedup, c.handleMessage)
}

func (c *AdminAuditConsumer) handleMessage(_ context.Context, data []byte) error {
	var event kafkamsg.AdminCronActionMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("admin audit consumer: dropping malformed message: %v", err)
		return nil // not retryable
	}

	row := &model.AdminAuditLog{
		Action:     event.Action,
		Service:    event.Service,
		CronName:   event.CronName,
		EmployeeID: event.EmployeeID,
		Reason:     event.Reason,
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
func (c *AdminAuditConsumer) Close() error {
	return c.reader.Close()
}
