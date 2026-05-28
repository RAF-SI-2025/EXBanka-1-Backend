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
}

// NewAdminAuditConsumer constructs an AdminAuditConsumer and configures the
// Kafka reader. The caller must call Start(ctx) to begin consuming.
func NewAdminAuditConsumer(brokers string, db *gorm.DB) *AdminAuditConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicAdminCronAction,
		GroupID:  "notification-service-admin-audit",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &AdminAuditConsumer{reader: reader, db: db}
}

// Start launches the consumer loop in a goroutine. It reads until ctx is
// cancelled, logging and continuing on transient errors.
func (c *AdminAuditConsumer) Start(ctx context.Context) {
	go func() {
		log.Println("admin audit consumer started, listening on", kafkamsg.TopicAdminCronAction)
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					log.Println("admin audit consumer shutting down")
					return
				}
				log.Printf("admin audit consumer: read error: %v", err)
				continue
			}
			c.handleMessage(msg.Value)
		}
	}()
}

func (c *AdminAuditConsumer) handleMessage(data []byte) {
	var event kafkamsg.AdminCronActionMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("admin audit consumer: unmarshal error: %v", err)
		return
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
		log.Printf("admin audit consumer: db insert error (action=%s service=%s cron=%s): %v",
			event.Action, event.Service, event.CronName, err)
	}
}

// Close releases the Kafka reader.
func (c *AdminAuditConsumer) Close() error {
	return c.reader.Close()
}
