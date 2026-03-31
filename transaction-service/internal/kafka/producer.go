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

func (p *Producer) PublishPaymentCreated(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error {
	return p.publish(ctx, kafkamsg.TopicPaymentCreated, msg)
}

func (p *Producer) PublishPaymentCompleted(ctx context.Context, msg kafkamsg.PaymentCompletedMessage) error {
	return p.publish(ctx, kafkamsg.TopicPaymentCompleted, msg)
}

func (p *Producer) PublishTransferCreated(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error {
	return p.publish(ctx, kafkamsg.TopicTransferCreated, msg)
}

func (p *Producer) PublishTransferCompleted(ctx context.Context, msg kafkamsg.TransferCompletedMessage) error {
	return p.publish(ctx, kafkamsg.TopicTransferCompleted, msg)
}

func (p *Producer) PublishPaymentFailed(ctx context.Context, msg kafkamsg.PaymentFailedMessage) error {
	return p.publish(ctx, kafkamsg.TopicPaymentFailed, msg)
}

func (p *Producer) PublishTransferFailed(ctx context.Context, msg kafkamsg.TransferFailedMessage) error {
	return p.publish(ctx, kafkamsg.TopicTransferFailed, msg)
}

func (p *Producer) PublishSagaDeadLetter(ctx context.Context, msg kafkamsg.SagaDeadLetterMessage) error {
	return p.publish(ctx, kafkamsg.TopicSagaDeadLetter, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
