package consumer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// These tests validate that the constructors build a wired-up consumer struct
// without dialing Kafka and that Close() unwinds cleanly. Start() requires a
// live broker and is left for integration tests.

func TestNewClientConsumer_BuildsAndCloses(t *testing.T) {
	c := NewClientConsumer("127.0.0.1:1", nil)
	assert.NotNil(t, c)
	// kafka-go's writer + reader Close must not block forever and not panic.
	c.Close()
}

func TestNewEmployeeConsumer_BuildsAndCloses(t *testing.T) {
	c := NewEmployeeConsumer("127.0.0.1:1", nil)
	assert.NotNil(t, c)
	c.Close()
}

func TestNewRolePermChangeConsumer_BuildsAndCloses(t *testing.T) {
	h := NewRolePermChangeHandler(&spyRevokeCache{}, &spyTokenRepo{}, time.Minute)
	c := NewRolePermChangeConsumer("127.0.0.1:1", h)
	assert.NotNil(t, c)
	require := assert.NoError
	require(t, c.Close())
}

// TestRolePermChangeConsumer_StartExitsOnCancelledContext: Start spawns a
// goroutine that loops on ReadMessage. With a cancelled context the goroutine
// should hit the ctx.Err() != nil branch and return. We can't observe the
// goroutine directly, but we can verify Start does not panic and Close cleans up.
func TestRolePermChangeConsumer_StartExitsOnCancelledContext(t *testing.T) {
	h := NewRolePermChangeHandler(&spyRevokeCache{}, &spyTokenRepo{}, time.Minute)
	c := NewRolePermChangeConsumer("127.0.0.1:1", h)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	c.Start(ctx)

	// Give the goroutine a moment to observe the cancellation and exit.
	time.Sleep(20 * time.Millisecond)
}

func TestClientConsumer_StartExitsOnCancelledContext(t *testing.T) {
	c := NewClientConsumer("127.0.0.1:1", nil)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Start(ctx)
	time.Sleep(20 * time.Millisecond)
}

func TestEmployeeConsumer_StartExitsOnCancelledContext(t *testing.T) {
	c := NewEmployeeConsumer("127.0.0.1:1", nil)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Start(ctx)
	time.Sleep(20 * time.Millisecond)
}
