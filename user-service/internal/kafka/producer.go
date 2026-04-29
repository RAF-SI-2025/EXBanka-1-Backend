package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with user-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishEmployeeCreated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicEmployeeCreated, msg)
}

func (p *Producer) PublishEmployeeUpdated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicEmployeeUpdated, msg)
}

func (p *Producer) PublishEmployeeLimitsUpdated(ctx context.Context, msg kafkamsg.EmployeeLimitsUpdatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicEmployeeLimitsUpdated, msg)
}

func (p *Producer) PublishLimitTemplate(ctx context.Context, msg kafkamsg.LimitTemplateMessage) error {
	var topic string
	switch msg.Action {
	case "created":
		topic = kafkamsg.TopicLimitTemplateCreated
	case "updated":
		topic = kafkamsg.TopicLimitTemplateUpdated
	default:
		topic = kafkamsg.TopicLimitTemplateDeleted
	}
	return p.inner.Publish(ctx, topic, msg)
}

func (p *Producer) PublishActuaryLimitUpdated(ctx context.Context, msg kafkamsg.ActuaryLimitUpdatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicActuaryLimitUpdated, msg)
}

func (p *Producer) PublishBlueprint(ctx context.Context, msg kafkamsg.BlueprintMessage) error {
	var topic string
	switch msg.Action {
	case "created":
		topic = kafkamsg.TopicBlueprintCreated
	case "updated":
		topic = kafkamsg.TopicBlueprintUpdated
	case "deleted":
		topic = kafkamsg.TopicBlueprintDeleted
	case "applied":
		topic = kafkamsg.TopicBlueprintApplied
	default:
		topic = kafkamsg.TopicBlueprintCreated
	}
	return p.inner.Publish(ctx, topic, msg)
}

func (p *Producer) PublishRolePermissionsChanged(ctx context.Context, msg kafkamsg.RolePermissionsChangedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicUserRolePermissionsChanged, msg)
}

// PublishRaw writes pre-serialized bytes to a topic. Used by the outbox
// relay so the relay can avoid double-encoding stored payloads.
func (p *Producer) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	return p.inner.PublishRaw(ctx, topic, payload)
}
