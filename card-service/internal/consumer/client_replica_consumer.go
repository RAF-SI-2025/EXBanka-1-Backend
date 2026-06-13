package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/exbanka/card-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// errMalformed is returned by handle when the event payload cannot be parsed.
// The Start loop uses errors.Is to skip retries for unparseable events.
var errMalformed = errors.New("malformed event payload")

// defaultBackoff is the sleep schedule between retry attempts (2 sleeps → 3 total attempts).
var defaultBackoff = []time.Duration{200 * time.Millisecond, 400 * time.Millisecond}

// replicaUpserter is the subset of ClientReplicaRepository the consumer needs.
type replicaUpserter interface {
	Upsert(ctx context.Context, in model.ClientReplica) error
}

// ClientReplicaConsumer maintains card-service's local client_replica from
// client.created / client.updated events (SP-1). Both topics carry the full
// client snapshot (ClientCreatedMessage), so a single handler serves both.
type ClientReplicaConsumer struct {
	reader  *kafka.Reader
	repo    replicaUpserter
	backoff []time.Duration // sleeps between retry attempts; len+1 = total attempts
}

// NewClientReplicaConsumer creates a consumer that subscribes to both
// client.created and client.updated topics via a single consumer group.
func NewClientReplicaConsumer(brokers string, repo replicaUpserter) *ClientReplicaConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{brokers},
		GroupTopics: []string{kafkamsg.TopicClientCreated, kafkamsg.TopicClientUpdated},
		GroupID:     "card-service-client-replica",
	})
	return &ClientReplicaConsumer{reader: r, repo: repo, backoff: defaultBackoff}
}

// handle parses one event payload and upserts the replica. Separated from the
// read loop so it can be unit-tested without Kafka.
// Returns errMalformed (wrapped) when the payload cannot be parsed, so callers
// can distinguish parse failures from transient upsert errors.
func (c *ClientReplicaConsumer) handle(ctx context.Context, value []byte) error {
	var evt kafkamsg.ClientCreatedMessage
	if err := json.Unmarshal(value, &evt); err != nil {
		return fmt.Errorf("%w: %v", errMalformed, err)
	}
	return c.repo.Upsert(ctx, model.ClientReplica{
		ID:        evt.ClientID,
		Email:     evt.Email,
		FirstName: evt.FirstName,
		LastName:  evt.LastName,
		JMBG:      evt.JMBG,
		Version:   evt.Version,
	})
}

// handleWithRetry calls handle and retries on transient (non-malformed) errors.
// Malformed payloads are logged and returned immediately without retrying.
// Retries up to len(c.backoff)+1 total attempts, sleeping c.backoff[i] between
// attempts. Each sleep honours ctx cancellation.
func (c *ClientReplicaConsumer) handleWithRetry(ctx context.Context, value []byte) error {
	maxAttempts := len(c.backoff) + 1
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := c.handle(ctx, value)
		if err == nil {
			return nil
		}
		if errors.Is(err, errMalformed) {
			log.Printf("client-replica consumer: malformed payload, skipping (no retry): %v", err)
			return err
		}
		lastErr = err
		log.Printf("client-replica consumer: upsert attempt %d/%d failed: %v", attempt+1, maxAttempts, err)
		if attempt < len(c.backoff) {
			select {
			case <-time.After(c.backoff[attempt]):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	log.Printf("client-replica consumer: all %d attempts exhausted, event dropped (will re-sync on next event): %v", maxAttempts, lastErr)
	return lastErr
}

// Start consumes messages in a background goroutine until ctx is cancelled.
func (c *ClientReplicaConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("client-replica consumer read error: %v", err)
				continue
			}
			if err := c.handleWithRetry(ctx, msg.Value); err != nil {
				log.Printf("client-replica consumer: final error for offset %d: %v", msg.Offset, err)
			}
		}
	}()
}

// Close shuts down the Kafka reader.
func (c *ClientReplicaConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("client-replica consumer close error: %v", err)
	}
}
