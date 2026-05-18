package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with client-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) PublishClientCreated(ctx context.Context, msg kafkamsg.ClientCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicClientCreated, msg)
}

func (p *Producer) PublishClientUpdated(ctx context.Context, msg kafkamsg.ClientCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicClientUpdated, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishClientLimitsUpdated(ctx context.Context, msg kafkamsg.ClientLimitsUpdatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicClientLimitsUpdated, msg)
}
