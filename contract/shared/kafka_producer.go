// Package shared — kafka_producer.go is the shared Kafka producer.
//
// Every service today has its own internal/kafka/producer.go that wraps a
// segmentio Writer with a private publish(topic, v) helper plus N typed
// PublishX(msg) methods. The wrapping is identical; the typed methods are
// just sugar. This shared producer keeps the wrapper once and lets each
// service add its own typed methods next to it (or just call Publish
// directly).
//
// Closing semantics: callers MUST call Close on shutdown so the underlying
// Writer flushes buffered messages. cmd/main.go typically does this with a
// defer right after construction.
package shared

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// HeaderIdempotencyKey is the Kafka message header carrying a per-message
// idempotency key. The shared producer stamps a fresh random key on every
// message (unless the caller already set one); the SAME key survives Kafka
// at-least-once redelivery, so consumers can dedup reprocessed messages.
// Consumers that don't care simply ignore it.
const HeaderIdempotencyKey = "idempotency-key"

// IdempotencyKeyFromHeaders returns the idempotency key a consumer should dedup
// on, or "" when the message carries none (older producers / replayed data).
func IdempotencyKeyFromHeaders(headers []kafkago.Header) string {
	for _, h := range headers {
		if h.Key == HeaderIdempotencyKey {
			return string(h.Value)
		}
	}
	return ""
}

func newIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// withIdempotencyKey returns msg with an idempotency-key header, generating one
// if the caller did not already provide it. Preserves any existing headers.
func withIdempotencyKey(msg kafkago.Message) kafkago.Message {
	for _, h := range msg.Headers {
		if h.Key == HeaderIdempotencyKey {
			return msg
		}
	}
	if k := newIdempotencyKey(); k != "" {
		msg.Headers = append(msg.Headers, kafkago.Header{Key: HeaderIdempotencyKey, Value: []byte(k)})
	}
	return msg
}

// Producer is the shared Kafka writer. Construct with NewProducer; call
// Publish to send a JSON-encoded message; call Close on shutdown.
//
// The producer is goroutine-safe — segmentio's Writer.WriteMessages is
// safe for concurrent use, and no per-call state is kept here.
type Producer struct {
	writer *kafkago.Writer
}

// ProducerConfig controls writer construction.
type ProducerConfig struct {
	// Brokers is a comma-separated list of broker addresses, the same
	// format the kafka-go Dial helpers expect.
	Brokers string

	// WriteTimeout caps how long a single WriteMessages call may block.
	// Zero defaults to 10s.
	WriteTimeout time.Duration

	// BatchTimeout is the max age of a buffered batch before it's flushed.
	// Zero leaves the segmentio default (1 second). Lower values reduce
	// latency at the cost of more network round-trips.
	BatchTimeout time.Duration

	// RequiredAcks controls durability. Zero defaults to RequireAll
	// (wait for ack from all in-sync replicas), the safe choice for a
	// banking system.
	RequiredAcks kafkago.RequiredAcks

	// Async, when true, makes WriteMessages return immediately without
	// waiting for broker acknowledgement. Use only for low-stakes
	// telemetry where loss is tolerable. Default is synchronous.
	Async bool
}

// NewProducer constructs a Producer with sensible banking-system defaults
// (synchronous, RequireAll, LeastBytes balancer).
func NewProducer(brokers string) *Producer {
	return NewProducerWithConfig(ProducerConfig{Brokers: brokers})
}

// NewProducerWithConfig constructs a Producer with explicit settings.
func NewProducerWithConfig(cfg ProducerConfig) *Producer {
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.RequiredAcks == 0 {
		cfg.RequiredAcks = kafkago.RequireAll
	}
	// segmentio/kafka-go treats a zero BatchTimeout as its 1-SECOND default, so a
	// synchronous single-message Publish (e.g. the session-created event on login)
	// blocks ~1s waiting for the batch window to elapse before flushing. Default to
	// a small window: synchronous publishes flush in ~10ms instead of ~1s, while
	// RequireAll durability is preserved (we still wait for the broker ack). Under
	// load, concurrent publishes within the window still batch.
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 10 * time.Millisecond
	}
	w := &kafkago.Writer{
		Addr:         kafkago.TCP(cfg.Brokers),
		Balancer:     &kafkago.LeastBytes{},
		WriteTimeout: cfg.WriteTimeout,
		BatchTimeout: cfg.BatchTimeout,
		RequiredAcks: cfg.RequiredAcks,
		Async:        cfg.Async,
	}
	return &Producer{writer: w}
}

// Publish JSON-encodes payload and sends it to topic. Returns the marshal
// error or the broker write error, never wraps them, so callers can match
// on the underlying type.
func (p *Producer) Publish(ctx context.Context, topic string, payload any) error {
	if p == nil || p.writer == nil {
		return errors.New("kafka: nil producer")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, withIdempotencyKey(kafkago.Message{Topic: topic, Value: data}))
}

// PublishWithKey is Publish with an explicit message key. Use when the
// topic is multi-partition and ordering by some natural id matters
// (e.g., per-account events).
func (p *Producer) PublishWithKey(ctx context.Context, topic string, key []byte, payload any) error {
	if p == nil || p.writer == nil {
		return errors.New("kafka: nil producer")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, withIdempotencyKey(kafkago.Message{Topic: topic, Key: key, Value: data}))
}

// PublishRaw sends a pre-serialized payload. Use when callers want full
// control over encoding (e.g., protobuf bytes).
func (p *Producer) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	if p == nil || p.writer == nil {
		return errors.New("kafka: nil producer")
	}
	return p.writer.WriteMessages(ctx, withIdempotencyKey(kafkago.Message{Topic: topic, Value: payload}))
}

// Close flushes pending messages and releases the underlying connection.
// Idempotent — repeated calls return nil after the first.
func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	w := p.writer
	p.writer = nil
	return w.Close()
}

// Writer returns the underlying segmentio Writer for advanced uses
// (custom headers, transactions). Most callers should not need this.
func (p *Producer) Writer() *kafkago.Writer { return p.writer }
