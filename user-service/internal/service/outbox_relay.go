package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/shared"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/repository"
)

// OutboxRelay drains the outbox table to Kafka. Polling-based; one goroutine
// is sufficient for current load. If multiple instances run, switch the
// repository's ClaimUnpublished to use FOR UPDATE SKIP LOCKED.
type OutboxRelay struct {
	repo     *repository.OutboxRepository
	producer *kafkaprod.Producer
	tick     time.Duration
}

func NewOutboxRelay(repo *repository.OutboxRepository, producer *kafkaprod.Producer, tick time.Duration) *OutboxRelay {
	if tick == 0 {
		tick = 2 * time.Second
	}
	return &OutboxRelay{repo: repo, producer: producer, tick: tick}
}

func (r *OutboxRelay) Start(ctx context.Context) {
	shared.RunScheduled(ctx, shared.ScheduledJob{
		Name:     "outbox-relay",
		Interval: r.tick,
		OnTick: func(ctx context.Context) error {
			r.processBatch(ctx)
			return nil
		},
	})
}

func (r *OutboxRelay) processBatch(ctx context.Context) {
	rows, err := r.repo.ClaimUnpublished(100)
	if err != nil {
		log.Printf("WARN: outbox claim failed: %v", err)
		return
	}
	for _, row := range rows {
		if r.producer != nil {
			if err := r.producer.PublishRaw(ctx, row.EventType, row.Payload); err != nil {
				log.Printf("WARN: outbox publish %s id=%d: %v", row.EventType, row.ID, err)
				continue
			}
		}
		if err := r.repo.MarkPublished(row.ID, time.Now().UTC()); err != nil {
			log.Printf("WARN: outbox mark-published id=%d: %v", row.ID, err)
		}
	}
}
