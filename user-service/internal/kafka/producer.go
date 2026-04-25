package kafka

import (
	"context"
	"encoding/json"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkago "github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafkago.Writer
}

func NewProducer(brokers string) *Producer {
	return &Producer{
		writer: &kafkago.Writer{
			Addr:     kafkago.TCP(brokers),
			Balancer: &kafkago.LeastBytes{},
		},
	}
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: kafkamsg.TopicSendEmail,
		Value: data,
	})
}

func (p *Producer) publish(ctx context.Context, topic string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: topic,
		Value: data,
	})
}

func (p *Producer) PublishEmployeeCreated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicEmployeeCreated, msg)
}

func (p *Producer) PublishEmployeeUpdated(ctx context.Context, msg kafkamsg.EmployeeCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicEmployeeUpdated, msg)
}

func (p *Producer) PublishEmployeeLimitsUpdated(ctx context.Context, msg kafkamsg.EmployeeLimitsUpdatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicEmployeeLimitsUpdated, msg)
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
	return p.publish(ctx, topic, msg)
}

func (p *Producer) PublishActuaryLimitUpdated(ctx context.Context, msg kafkamsg.ActuaryLimitUpdatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicActuaryLimitUpdated, msg)
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
	return p.publish(ctx, topic, msg)
}

func (p *Producer) PublishRolePermissionsChanged(ctx context.Context, msg kafkamsg.RolePermissionsChangedMessage) error {
	return p.publish(ctx, kafkamsg.TopicUserRolePermissionsChanged, msg)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
