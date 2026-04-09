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

func (p *Producer) Publish(ctx context.Context, topic string, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: topic,
		Value: data,
	})
}

func (p *Producer) PublishEmailSent(ctx context.Context, msg kafkamsg.EmailSentMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: kafkamsg.TopicEmailSent,
		Value: data,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
