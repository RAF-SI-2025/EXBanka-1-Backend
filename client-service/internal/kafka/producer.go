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

func (p *Producer) PublishClientCreated(ctx context.Context, msg kafkamsg.ClientCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicClientCreated, msg)
}

func (p *Producer) PublishClientUpdated(ctx context.Context, msg kafkamsg.ClientCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicClientUpdated, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishClientLimitsUpdated(ctx context.Context, msg kafkamsg.ClientLimitsUpdatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicClientLimitsUpdated, msg)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
