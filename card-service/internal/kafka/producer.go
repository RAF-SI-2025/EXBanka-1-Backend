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

func (p *Producer) publish(ctx context.Context, topic string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: topic,
		Value: data,
	})
}

func (p *Producer) PublishCardCreated(ctx context.Context, msg kafkamsg.CardCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicCardCreated, msg)
}

func (p *Producer) PublishCardStatusChanged(ctx context.Context, msg kafkamsg.CardStatusChangedMessage) error {
	return p.publish(ctx, kafkamsg.TopicCardStatusChanged, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishCardTemporaryBlocked(ctx context.Context, msg kafkamsg.CardTemporaryBlockedMessage) error {
	return p.publish(ctx, kafkamsg.TopicCardTemporaryBlocked, msg)
}

func (p *Producer) PublishVirtualCardCreated(ctx context.Context, msg kafkamsg.VirtualCardCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVirtualCardCreated, msg)
}

func (p *Producer) PublishCardRequestCreated(ctx context.Context, msg kafkamsg.CardRequestCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicCardRequestCreated, msg)
}

func (p *Producer) PublishCardRequestApproved(ctx context.Context, msg kafkamsg.CardRequestApprovedMessage) error {
	return p.publish(ctx, kafkamsg.TopicCardRequestApproved, msg)
}

func (p *Producer) PublishCardRequestRejected(ctx context.Context, msg kafkamsg.CardRequestRejectedMessage) error {
	return p.publish(ctx, kafkamsg.TopicCardRequestRejected, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	return p.publish(ctx, kafkamsg.TopicGeneralNotification, msg)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
