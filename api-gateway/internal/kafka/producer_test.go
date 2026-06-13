package kafka

import (
	"context"
	"testing"
)

// The publishes target an unreachable broker (connection refused, ~1ms) so the
// best-effort emit paths run end-to-end (marshal + write attempt + swallowed
// error) without needing a live Kafka.
const deadBroker = "127.0.0.1:1"

func TestAuditProducer_PublishCronAction(t *testing.T) {
	p := NewAuditProducer(deadBroker)
	defer func() { _ = p.Close() }()
	// Must not panic and must return even though the broker is unreachable.
	p.PublishCronAction(context.Background(), "trigger", "stock-service", "tax", 7, "manual")
}

func TestAuditProducer_PublishBusinessAction(t *testing.T) {
	p := NewAuditProducer(deadBroker)
	defer func() { _ = p.Close() }()
	p.PublishBusinessAction(context.Background(), "limit.set", 7, "employee", "42", "max=1000")
}

func TestAuditProducer_PublishBusinessAction_NilReceiverIsSafe(t *testing.T) {
	var p *AuditProducer
	// PublishBusinessAction guards against a nil receiver.
	p.PublishBusinessAction(context.Background(), "tax.collect", 1, "tax", "x", "")
}

func TestAuditProducer_CloseIdempotent(t *testing.T) {
	p := NewAuditProducer(deadBroker)
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
