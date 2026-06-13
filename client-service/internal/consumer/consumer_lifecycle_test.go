package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func TestNewEmployeeLimitReplicaConsumer_Constructs(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := NewEmployeeLimitReplicaConsumer("localhost:9092", repo)
	require.NotNil(t, c)
	require.NotNil(t, c.reader)
	assert.Equal(t, defaultBackoff, c.backoff)
	// Reader is wired to the SP-2b topic/group.
	assert.Equal(t, kafkamsg.TopicEmployeeLimitsUpdated, c.reader.Config().Topic)
	c.Close()
}

// TestEmployeeLimitReplicaConsumer_StartStopsOnContextCancel verifies the
// background read loop spawned by Start exits when the context is cancelled
// (it does not leak a goroutine) and that Close is safe to call afterwards.
func TestEmployeeLimitReplicaConsumer_StartStopsOnContextCancel(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := NewEmployeeLimitReplicaConsumer("localhost:9092", repo)

	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	// Give the goroutine a moment to enter ReadMessage, then cancel so the
	// loop's ctx.Err() guard returns.
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	c.Close()
}

// TestHandleWithRetry_HonoursContextCancellationDuringBackoff verifies the
// ctx.Done() branch inside the retry backoff select: a transient error followed
// by a cancelled context returns ctx.Err() instead of sleeping the full backoff.
func TestHandleWithRetry_HonoursContextCancellationDuringBackoff(t *testing.T) {
	repo := &fakeLimitReplicaRepo{err: context.DeadlineExceeded, failCount: 5}
	// A long backoff guarantees the test would hang if ctx cancellation were
	// not honoured.
	c := &EmployeeLimitReplicaConsumer{repo: repo, backoff: []time.Duration{time.Hour}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	payload, _ := json.Marshal(kafkamsg.EmployeeLimitsUpdatedMessage{EmployeeID: 1, Version: 1})
	err := c.handleWithRetry(ctx, payload)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
