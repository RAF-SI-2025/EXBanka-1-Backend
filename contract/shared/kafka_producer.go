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
	"encoding/json"
	"errors"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

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
	return p.writer.WriteMessages(ctx, kafkago.Message{Topic: topic, Value: data})
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
	return p.writer.WriteMessages(ctx, kafkago.Message{Topic: topic, Key: key, Value: data})
}

// PublishRaw sends a pre-serialized payload. Use when callers want full
// control over encoding (e.g., protobuf bytes).
func (p *Producer) PublishRaw(ctx context.Context, topic string, payload []byte) error {
	if p == nil || p.writer == nil {
		return errors.New("kafka: nil producer")
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{Topic: topic, Value: payload})
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
