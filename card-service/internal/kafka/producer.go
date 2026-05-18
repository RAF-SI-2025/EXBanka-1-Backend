package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with card-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) PublishCardCreated(ctx context.Context, msg kafkamsg.CardCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicCardCreated, msg)
}

func (p *Producer) PublishCardStatusChanged(ctx context.Context, msg kafkamsg.CardStatusChangedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicCardStatusChanged, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishCardTemporaryBlocked(ctx context.Context, msg kafkamsg.CardTemporaryBlockedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicCardTemporaryBlocked, msg)
}

func (p *Producer) PublishVirtualCardCreated(ctx context.Context, msg kafkamsg.VirtualCardCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicVirtualCardCreated, msg)
}

func (p *Producer) PublishCardRequestCreated(ctx context.Context, msg kafkamsg.CardRequestCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicCardRequestCreated, msg)
}

func (p *Producer) PublishCardRequestApproved(ctx context.Context, msg kafkamsg.CardRequestApprovedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicCardRequestApproved, msg)
}

func (p *Producer) PublishCardRequestRejected(ctx context.Context, msg kafkamsg.CardRequestRejectedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicCardRequestRejected, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicGeneralNotification, msg)
}
