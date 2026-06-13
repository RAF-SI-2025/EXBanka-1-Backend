package kafka

import (
	"context"

	contract "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with stock-service typed
// publish methods. The exported Publish remains for callers that publish
// to dynamic topics (e.g., cross-bank saga lifecycle events).
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

// Publish forwards to the shared producer for arbitrary topics.
func (p *Producer) Publish(ctx context.Context, topic string, msg any) error {
	return p.inner.Publish(ctx, topic, msg)
}

// PublishRaw forwards a pre-serialized payload (e.g., a hand-marshaled
// JSON envelope from the cross-bank saga executor or outbox relay).
func (p *Producer) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	return p.inner.PublishRaw(ctx, topic, payload)
}

func (p *Producer) PublishSecuritySynced(ctx context.Context, msg contract.SecuritySyncedMessage) error {
	return p.inner.Publish(ctx, contract.TopicSecuritySynced, msg)
}

func (p *Producer) PublishListingUpdated(ctx context.Context, msg contract.ListingUpdatedMessage) error {
	return p.inner.Publish(ctx, contract.TopicListingUpdated, msg)
}

func (p *Producer) PublishOrderCreated(ctx context.Context, msg interface{}) error {
	return p.inner.Publish(ctx, contract.TopicOrderCreated, msg)
}

func (p *Producer) PublishOrderApproved(ctx context.Context, msg interface{}) error {
	return p.inner.Publish(ctx, contract.TopicOrderApproved, msg)
}

func (p *Producer) PublishOrderDeclined(ctx context.Context, msg interface{}) error {
	return p.inner.Publish(ctx, contract.TopicOrderDeclined, msg)
}

func (p *Producer) PublishOrderFilled(ctx context.Context, msg interface{}) error {
	return p.inner.Publish(ctx, contract.TopicOrderFilled, msg)
}

func (p *Producer) PublishOrderCancelled(ctx context.Context, msg interface{}) error {
	return p.inner.Publish(ctx, contract.TopicOrderCancelled, msg)
}

func (p *Producer) PublishHoldingUpdated(ctx context.Context, msg contract.HoldingUpdatedMessage) error {
	return p.inner.Publish(ctx, contract.TopicHoldingUpdated, msg)
}

func (p *Producer) PublishOTCTradeExecuted(ctx context.Context, msg contract.OTCTradeMessage) error {
	return p.inner.Publish(ctx, contract.TopicOTCTradeExecuted, msg)
}

func (p *Producer) PublishTaxCollected(ctx context.Context, msg contract.TaxCollectedMessage) error {
	return p.inner.Publish(ctx, contract.TopicTaxCollected, msg)
}

func (p *Producer) PublishOptionExercised(ctx context.Context, msg contract.OptionExercisedMessage) error {
	return p.inner.Publish(ctx, contract.TopicOptionExercised, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg contract.GeneralNotificationMessage) error {
	return p.inner.Publish(ctx, contract.TopicGeneralNotification, msg)
}

// PublishSagaDeadLetter publishes a stuck-saga dead-letter event to the
// stock-service dead-letter topic. Called by SagaRecovery when a step
// exhausts maxSagaRecoveryRetries.
func (p *Producer) PublishSagaDeadLetter(ctx context.Context, msg contract.SagaDeadLetterMessage) error {
	return p.inner.Publish(ctx, contract.TopicStockSagaDeadLetter, msg)
}

// PublishWatchlistAlert publishes a WatchlistPriceMoveMessage when a
// watchlisted ticker has a daily price move exceeding the configured threshold.
func (p *Producer) PublishWatchlistAlert(ctx context.Context, msg contract.WatchlistPriceMoveMessage) error {
	return p.inner.Publish(ctx, contract.TopicWatchlistAlert, msg)
}
