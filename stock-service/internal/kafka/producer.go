package kafka

import (
	"context"
	"encoding/json"

	kafkalib "github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafkalib.Writer
}

func NewProducer(brokers string) *Producer {
	return &Producer{
		writer: &kafkalib.Writer{
			Addr:     kafkalib.TCP(brokers),
			Balancer: &kafkalib.LeastBytes{},
		},
	}
}

func (p *Producer) Publish(ctx context.Context, topic string, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkalib.Message{
		Topic: topic,
		Value: data,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
