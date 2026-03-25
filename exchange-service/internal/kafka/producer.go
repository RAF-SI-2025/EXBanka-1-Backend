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
	return p.writer.WriteMessages(ctx, kafkago.Message{Topic: topic, Value: data})
}

func (p *Producer) PublishRatesUpdated(ctx context.Context, currenciesUpdated []string, updatedAt string) error {
	return p.publish(ctx, kafkamsg.TopicExchangeRatesUpdated, kafkamsg.ExchangeRatesUpdatedMessage{
		CurrenciesUpdated: currenciesUpdated,
		UpdatedAt:         updatedAt,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
