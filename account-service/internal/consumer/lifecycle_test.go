package consumer

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/account-service/internal/model"
)

// noopReplicaRepo is a do-nothing replicaUpserter for lifecycle wiring tests.
type noopReplicaRepo struct{}

func (noopReplicaRepo) Upsert(context.Context, model.ClientReplica) error { return nil }

// TestClientLimitConsumer_Lifecycle covers NewClientLimitConsumer (reader wiring),
// Start, and Close — none of which need a live broker. kafka.NewReader is lazy,
// and Start's read loop is given an already-cancelled context so its first
// ReadMessage returns immediately and the goroutine exits via the ctx.Err()
// branch.
func TestClientLimitConsumer_Lifecycle(t *testing.T) {
	c := NewClientLimitConsumer("localhost:9092", &fakePolicyRepo{}, &fakeApplier{})
	if c == nil || c.reader == nil {
		t.Fatal("NewClientLimitConsumer must wire a Kafka reader")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: the read loop exits on its first ReadMessage
	c.Start(ctx)
	time.Sleep(20 * time.Millisecond) // let the goroutine observe cancellation
	c.Close()
}

// TestClientReplicaConsumer_Lifecycle is the replica-consumer counterpart.
func TestClientReplicaConsumer_Lifecycle(t *testing.T) {
	c := NewClientReplicaConsumer("localhost:9092", noopReplicaRepo{})
	if c == nil || c.reader == nil {
		t.Fatal("NewClientReplicaConsumer must wire a Kafka reader")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	c.Close()
}
