package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with exchange-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) PublishRatesUpdated(ctx context.Context, currenciesUpdated []string, updatedAt string) error {
	return p.inner.Publish(ctx, kafkamsg.TopicExchangeRatesUpdated, kafkamsg.ExchangeRatesUpdatedMessage{
		CurrenciesUpdated: currenciesUpdated,
		UpdatedAt:         updatedAt,
	})
}
