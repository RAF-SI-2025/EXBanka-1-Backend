// Package kafka provides the api-gateway Kafka audit publisher.
// The gateway does not consume Kafka; it only publishes admin audit events
// (e.g. admin cron trigger/pause/resume actions) to the admin.cron-action
// topic so the notification-service can persist an audit log row.
package kafka

import (
	"context"
	"log"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// AuditProducer wraps the shared producer with typed publish methods for
// admin audit events.
type AuditProducer struct {
	inner *shared.Producer
}

// NewAuditProducer constructs an AuditProducer connected to brokers.
// The caller must call Close() on shutdown.
func NewAuditProducer(brokers string) *AuditProducer {
	return &AuditProducer{inner: shared.NewProducer(brokers)}
}

// Close flushes pending messages and releases the connection.
func (p *AuditProducer) Close() error { return p.inner.Close() }

// PublishCronAction publishes an AdminCronActionMessage to the
// admin.cron-action topic. Failures are logged but not propagated — the
// audit trail is best-effort and must not block the HTTP response.
func (p *AuditProducer) PublishCronAction(ctx context.Context, action, service, cronName string, employeeID int64, reason string) {
	msg := kafkamsg.AdminCronActionMessage{
		Action:     action,
		Service:    service,
		CronName:   cronName,
		EmployeeID: employeeID,
		Timestamp:  time.Now().UTC(),
		Reason:     reason,
	}
	if err := p.inner.Publish(ctx, kafkamsg.TopicAdminCronAction, msg); err != nil {
		log.Printf("audit producer: failed to publish cron action (action=%s service=%s cron=%s): %v",
			action, service, cronName, err)
	}
}
