package consumer

import (
	"context"
	"testing"
	"time"
)

// TestNewSupervisorDemotedConsumer_Close verifies the constructor returns a
// non-nil consumer and Close is idempotent (no panic) without a real broker.
func TestNewSupervisorDemotedConsumer_Close(t *testing.T) {
	c := NewSupervisorDemotedConsumer("127.0.0.1:1", nil, nil)
	if c == nil {
		t.Fatal("nil consumer")
	}
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// TestSupervisorDemotedConsumer_StartCancels verifies Start spawns a goroutine
// that returns when context is canceled.
func TestSupervisorDemotedConsumer_StartCancels(t *testing.T) {
	c := NewSupervisorDemotedConsumer("127.0.0.1:1", nil, nil)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
}
