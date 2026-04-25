package kafka

import (
	"context"
	"encoding/json"

	contract "github.com/exbanka/contract/kafka"
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

// PublishRaw writes pre-serialized bytes to a topic.
func (p *Producer) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	return p.writer.WriteMessages(ctx, kafkalib.Message{
		Topic: topic,
		Value: payload,
	})
}

func (p *Producer) PublishSecuritySynced(ctx context.Context, msg contract.SecuritySyncedMessage) error {
	return p.Publish(ctx, contract.TopicSecuritySynced, msg)
}

func (p *Producer) PublishListingUpdated(ctx context.Context, msg contract.ListingUpdatedMessage) error {
	return p.Publish(ctx, contract.TopicListingUpdated, msg)
}

func (p *Producer) PublishOrderCreated(ctx context.Context, msg interface{}) error {
	return p.Publish(ctx, contract.TopicOrderCreated, msg)
}

func (p *Producer) PublishOrderApproved(ctx context.Context, msg interface{}) error {
	return p.Publish(ctx, contract.TopicOrderApproved, msg)
}

func (p *Producer) PublishOrderDeclined(ctx context.Context, msg interface{}) error {
	return p.Publish(ctx, contract.TopicOrderDeclined, msg)
}

func (p *Producer) PublishOrderFilled(ctx context.Context, msg interface{}) error {
	return p.Publish(ctx, contract.TopicOrderFilled, msg)
}

func (p *Producer) PublishOrderCancelled(ctx context.Context, msg interface{}) error {
	return p.Publish(ctx, contract.TopicOrderCancelled, msg)
}

func (p *Producer) PublishHoldingUpdated(ctx context.Context, msg contract.HoldingUpdatedMessage) error {
	return p.Publish(ctx, contract.TopicHoldingUpdated, msg)
}

func (p *Producer) PublishOTCTradeExecuted(ctx context.Context, msg contract.OTCTradeMessage) error {
	return p.Publish(ctx, contract.TopicOTCTradeExecuted, msg)
}

func (p *Producer) PublishTaxCollected(ctx context.Context, msg contract.TaxCollectedMessage) error {
	return p.Publish(ctx, contract.TopicTaxCollected, msg)
}

func (p *Producer) PublishOptionExercised(ctx context.Context, msg contract.OptionExercisedMessage) error {
	return p.Publish(ctx, contract.TopicOptionExercised, msg)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
