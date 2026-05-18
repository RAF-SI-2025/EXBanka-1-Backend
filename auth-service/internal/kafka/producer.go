package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with auth-service typed
// publish methods. The exported Publish remains because auth_service.go
// publishes to a variety of dynamic topics (sessions, account-status).
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

// SendEmail publishes a generic email request.
func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

// Publish forwards to the shared producer for callers that publish to
// dynamic topics (auth session events, mobile-device events).
func (p *Producer) Publish(ctx context.Context, topic string, msg any) error {
	return p.inner.Publish(ctx, topic, msg)
}
