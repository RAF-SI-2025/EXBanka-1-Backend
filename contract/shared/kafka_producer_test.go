package shared

import (
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// TestNewProducer_DefaultsBatchTimeout guards against the segmentio/kafka-go
// gotcha where a zero BatchTimeout falls back to the library's 1-SECOND
// default, making every synchronous single-message Publish block ~1s (this was
// the cause of ~1s logins — the session-created event publishes in-path). The
// shared constructor must default BatchTimeout to a small window.
func TestNewProducer_DefaultsBatchTimeout(t *testing.T) {
	p := NewProducer("localhost:9092")
	defer func() { _ = p.Close() }()

	if p.writer == nil {
		t.Fatal("writer is nil")
	}
	if p.writer.BatchTimeout <= 0 || p.writer.BatchTimeout >= time.Second {
		t.Fatalf("BatchTimeout = %v; want a small (<1s) non-zero default to avoid the 1s kafka-go batch stall", p.writer.BatchTimeout)
	}
	// Banking defaults preserved.
	if p.writer.RequiredAcks != kafkago.RequireAll {
		t.Errorf("RequiredAcks = %v; want RequireAll", p.writer.RequiredAcks)
	}
}

// TestNewProducerWithConfig_HonorsExplicitBatchTimeout confirms an explicit
// BatchTimeout is not overridden by the default.
func TestNewProducerWithConfig_HonorsExplicitBatchTimeout(t *testing.T) {
	p := NewProducerWithConfig(ProducerConfig{Brokers: "localhost:9092", BatchTimeout: 250 * time.Millisecond})
	defer func() { _ = p.Close() }()

	if p.writer.BatchTimeout != 250*time.Millisecond {
		t.Fatalf("BatchTimeout = %v; want the explicit 250ms", p.writer.BatchTimeout)
	}
}
