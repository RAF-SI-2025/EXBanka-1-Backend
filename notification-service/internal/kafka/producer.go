package kafka

import (
	"context"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with notification-service typed
// publish methods. The exported Publish stays for the verification consumer
// which forwards challenge events to the mobile-push topic with a dynamic
// payload.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

// Publish forwards to the shared producer for callers that need an
// arbitrary topic.
func (p *Producer) Publish(ctx context.Context, topic string, msg any) error {
	return p.inner.Publish(ctx, topic, msg)
}

func (p *Producer) PublishEmailSent(ctx context.Context, msg kafkamsg.EmailSentMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicEmailSent, msg)
}

// WriteDeadLetter publishes a message that exhausted its consumer retries to the
// notification.dead-letter topic, so it is preserved for inspection/replay
// instead of being silently dropped. Satisfies consumer.DeadLetterWriter.
func (p *Producer) WriteDeadLetter(ctx context.Context, source string, value []byte, cause string) error {
	return p.inner.Publish(ctx, kafkamsg.TopicNotificationDeadLetter, kafkamsg.NotificationDeadLetterMessage{
		Source:   source,
		Payload:  string(value),
		Error:    cause,
		FailedAt: time.Now().Unix(),
	})
}
