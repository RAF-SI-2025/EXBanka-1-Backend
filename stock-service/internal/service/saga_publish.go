package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/outbox"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
)

// publishSagaEvent enqueues a Kafka event into the transactional outbox if
// the outbox is wired (durable: a crash between business commit and Kafka
// publish no longer drops events). Falls back to the legacy direct
// producer.PublishRaw path when the outbox is nil so unit tests that don't
// spin up a DB still work.
//
// sagaID is stamped on the outbox row so cross-service audit can correlate
// Kafka events to the originating saga (joined against side-effect tables
// like account_ledger_entries.saga_id and stock_holdings.saga_id).
//
// This is the canonical post-saga publish primitive used by every saga
// service in stock-service. See OTCOfferService.publishViaOutboxOrDirect
// for the original / inline equivalent (kept on that service for binary
// compatibility with existing tests).
func publishSagaEvent(
	ctx context.Context,
	ob *outbox.Outbox,
	obDB *gorm.DB,
	producer *kafkaprod.Producer,
	topic string,
	payload []byte,
	sagaID string,
) {
	if ob != nil && obDB != nil {
		_ = ob.Enqueue(obDB, topic, payload, sagaID)
		return
	}
	if producer != nil {
		_ = producer.PublishRaw(ctx, topic, payload)
	}
}
