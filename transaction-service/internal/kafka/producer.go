package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with transaction-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) PublishPaymentCreated(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicPaymentCreated, msg)
}

func (p *Producer) PublishPaymentCompleted(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicPaymentCompleted, msg)
}

func (p *Producer) PublishTransferCreated(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicTransferCreated, msg)
}

func (p *Producer) PublishTransferCompleted(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicTransferCompleted, msg)
}

func (p *Producer) PublishPaymentFailed(ctx context.Context, msg kafkamsg.PaymentFailedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicPaymentFailed, msg)
}

func (p *Producer) PublishTransferFailed(ctx context.Context, msg kafkamsg.TransferFailedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicTransferFailed, msg)
}

func (p *Producer) PublishSagaDeadLetter(ctx context.Context, msg kafkamsg.SagaDeadLetterMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSagaDeadLetter, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicGeneralNotification, msg)
}
