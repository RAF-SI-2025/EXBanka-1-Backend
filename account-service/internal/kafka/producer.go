package kafka

import (
	"context"
	"encoding/json"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkago "github.com/segmentio/kafka-go"
)

type AccountStatusChangedMsg struct {
	AccountNumber string `json:"account_number"`
	Status        string `json:"status"`
	OwnerEmail    string `json:"owner_email"`
}

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

func (p *Producer) PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: kafkamsg.TopicAccountCreated,
		Value: data,
	})
}

func (p *Producer) PublishAccountStatusChanged(ctx context.Context, msg AccountStatusChangedMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: kafkamsg.TopicAccountStatusChanged,
		Value: data,
	})
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

func (p *Producer) Close() error {
	return p.writer.Close()
}
