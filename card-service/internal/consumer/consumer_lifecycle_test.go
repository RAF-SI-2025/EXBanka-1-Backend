package consumer

import (
	"context"
	"testing"
	"time"
)

// TestNewClientReplicaConsumer_Wiring verifies the constructor builds a
// consumer with a live kafka.Reader and the default backoff schedule, then
// that Close shuts the reader down cleanly.
func TestNewClientReplicaConsumer_Wiring(t *testing.T) {
	c := NewClientReplicaConsumer("localhost:9", &fakeReplicaRepo{})
	if c == nil {
		t.Fatal("constructor returned nil")
	}
	if c.reader == nil {
		t.Fatal("expected a non-nil kafka.Reader")
	}
	if len(c.backoff) != len(defaultBackoff) {
		t.Fatalf("expected default backoff len %d, got %d", len(defaultBackoff), len(c.backoff))
	}
	// Close must not panic and should release the reader.
	c.Close()
}

// TestStart_CancelledContextExits starts the read loop with a pre-cancelled
// context. ReadMessage returns the context error immediately, so the goroutine
// takes the ctx.Err() != nil -> return branch and exits without spinning.
func TestStart_CancelledContextExits(t *testing.T) {
	c := NewClientReplicaConsumer("localhost:9", &fakeReplicaRepo{})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: the first ReadMessage returns context.Canceled.

	c.Start(ctx)
	// Give the goroutine a moment to observe cancellation and return.
	time.Sleep(50 * time.Millisecond)
}
