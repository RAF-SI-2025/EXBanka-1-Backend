package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// errMalformed is returned by handle when the event payload cannot be parsed.
// The handleWithRetry loop uses errors.Is to skip retries for unparseable events.
var errMalformed = errors.New("malformed event payload")

// defaultBackoff is the sleep schedule between retry attempts (2 sleeps → 3 total attempts).
var defaultBackoff = []time.Duration{200 * time.Millisecond, 400 * time.Millisecond}

// policyUpserter is the subset of ClientLimitPolicyRepository the consumer needs.
type policyUpserter interface {
	Upsert(ctx context.Context, in model.ClientLimitPolicy) (bool, error)
	GetByClientID(ctx context.Context, id uint64) (model.ClientLimitPolicy, error)
}

// limitApplier is the subset of AccountService the consumer needs.
type limitApplier interface {
	ApplyClientLimitPolicy(ctx context.Context, clientID uint64, daily, monthly decimal.Decimal, changedBy int64) error
}

// ClientLimitConsumer maintains per-account DailyLimit/MonthlyLimit caps by
// consuming client.limits-updated events (SP-5).
type ClientLimitConsumer struct {
	reader  *kafka.Reader
	repo    policyUpserter
	applier limitApplier
	backoff []time.Duration // sleeps between retry attempts; len+1 = total attempts
}

// NewClientLimitConsumer creates a consumer that subscribes to the
// client.limits-updated topic via a dedicated consumer group.
func NewClientLimitConsumer(brokers string, repo policyUpserter, applier limitApplier) *ClientLimitConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers},
		Topic:   kafkamsg.TopicClientLimitsUpdated,
		GroupID: "account-service-client-limit",
	})
	return &ClientLimitConsumer{reader: r, repo: repo, applier: applier, backoff: defaultBackoff}
}

// parseDecimalOrZero parses s as a decimal. An empty string is a legitimate
// "no limit set" value and returns decimal.Zero silently. A non-empty but
// unparseable string is a producer-side data corruption and logs a warning
// before returning decimal.Zero.
func parseDecimalOrZero(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		log.Printf("client-limit consumer: unparseable decimal %q, treating as zero: %v", s, err)
		return decimal.Zero
	}
	return d
}

// handle parses one event and applies the client limit policy.
// RETRY-SAFE: distinguishes stale events (skip) from duplicate-of-current (re-apply).
// Returns errMalformed (wrapped) when the payload cannot be JSON-decoded.
func (c *ClientLimitConsumer) handle(ctx context.Context, value []byte) error {
	var evt kafkamsg.ClientLimitsUpdatedMessage
	if err := json.Unmarshal(value, &evt); err != nil {
		return fmt.Errorf("%w: %v", errMalformed, err)
	}

	daily := parseDecimalOrZero(evt.DailyLimit)
	monthly := parseDecimalOrZero(evt.MonthlyLimit)
	clientID := uint64(evt.ClientID)

	applied, err := c.repo.Upsert(ctx, model.ClientLimitPolicy{
		ClientID:     clientID,
		DailyLimit:   daily,
		MonthlyLimit: monthly,
		Version:      evt.Version,
	})
	if err != nil {
		return err // retry
	}

	if !applied {
		// Not strictly newer. Could be (a) stale older event, or (b) a duplicate of the
		// CURRENT version being retried after a prior apply failure. Distinguish:
		policy, gerr := c.repo.GetByClientID(ctx, clientID)
		if gerr != nil {
			return gerr // retry
		}
		if policy.Version != evt.Version {
			return nil // stale → skip, do not apply
		}
		// else: duplicate of current version → fall through and (re)apply (idempotent)
	}

	return c.applier.ApplyClientLimitPolicy(ctx, clientID, daily, monthly, evt.SetByEmployee) // err → retry
}

// handleWithRetry calls handle and retries on transient (non-malformed) errors.
// Malformed payloads are logged and returned immediately without retrying.
// Retries up to len(c.backoff)+1 total attempts, sleeping c.backoff[i] between
// attempts. Each sleep honours ctx cancellation.
func (c *ClientLimitConsumer) handleWithRetry(ctx context.Context, value []byte) error {
	maxAttempts := len(c.backoff) + 1
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := c.handle(ctx, value)
		if err == nil {
			return nil
		}
		if errors.Is(err, errMalformed) {
			log.Printf("client-limit consumer: malformed payload, skipping (no retry): %v", err)
			return err
		}
		lastErr = err
		log.Printf("client-limit consumer: apply attempt %d/%d failed: %v", attempt+1, maxAttempts, err)
		if attempt < len(c.backoff) {
			select {
			case <-time.After(c.backoff[attempt]):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	log.Printf("client-limit consumer: all %d attempts exhausted, event dropped: %v", maxAttempts, lastErr)
	return lastErr
}

// Start consumes messages in a background goroutine until ctx is cancelled.
func (c *ClientLimitConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("client-limit consumer read error: %v", err)
				continue
			}
			if err := c.handleWithRetry(ctx, msg.Value); err != nil {
				log.Printf("client-limit consumer: final error for offset %d: %v", msg.Offset, err)
			}
		}
	}()
}

// Close shuts down the Kafka reader.
func (c *ClientLimitConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("client-limit consumer close error: %v", err)
	}
}
