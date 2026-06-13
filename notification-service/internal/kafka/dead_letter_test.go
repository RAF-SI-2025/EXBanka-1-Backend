package kafka

import (
	"context"
	"testing"
)

// TestProducer_WriteDeadLetter exercises the dead-letter publish path. With a
// cancelled context the underlying publish returns an error, but the method
// body (building the NotificationDeadLetterMessage and forwarding it) runs.
func TestProducer_WriteDeadLetter(t *testing.T) {
	p := NewProducer("127.0.0.1:1")
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// We don't assert the (expected) transport error — only that the method is
	// callable and does not panic while constructing/forwarding the message.
	_ = p.WriteDeadLetter(ctx, "email", []byte(`{"to":"x@y.z"}`), "boom")
}
