package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
)

// TestNewClientReplicaConsumer_ConstructsAndCloses verifies the constructor wires
// a Kafka reader (no broker connection is made until ReadMessage) and that Close
// shuts the reader down cleanly.
func TestNewClientReplicaConsumer_ConstructsAndCloses(t *testing.T) {
	repo := &fakeClientReplicaRepo{}
	c := NewClientReplicaConsumer("localhost:9999", repo)
	if c == nil || c.reader == nil {
		t.Fatal("expected a constructed consumer with a reader")
	}
	c.Close() // must not panic on a never-started reader
}

// TestClientReplicaConsumer_Start_ExitsOnCancelledContext verifies Start's read
// loop returns promptly when the context is already cancelled (no broker needed).
func TestClientReplicaConsumer_Start_ExitsOnCancelledContext(t *testing.T) {
	repo := &fakeClientReplicaRepo{}
	c := NewClientReplicaConsumer("localhost:9999", repo)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Start(ctx)
	// Give the goroutine a moment to observe the cancelled context and return.
	time.Sleep(50 * time.Millisecond)
	if repo.calls != 0 {
		t.Fatalf("no messages should have been handled, got %d calls", repo.calls)
	}
}

func TestClientReplicaHandleWithRetry_ExhaustsRetries(t *testing.T) {
	repo := &fakeClientReplicaRepo{err: errMockClientTransient, failCount: 99}
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{ClientID: 1, Version: 1})

	err := c.handleWithRetry(context.Background(), payload)
	if err == nil {
		t.Fatal("expected the last error after all attempts are exhausted")
	}
	if repo.calls != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 backoffs), got %d", repo.calls)
	}
}

func TestClientReplicaHandleWithRetry_CancelDuringBackoff(t *testing.T) {
	repo := &fakeClientReplicaRepo{err: errMockClientTransient, failCount: 99}
	// A long backoff so the ctx-cancellation branch wins the select deterministically.
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{time.Hour}}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{ClientID: 1, Version: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.handleWithRetry(ctx, payload)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled from interrupted backoff, got %v", err)
	}
	if repo.calls != 1 {
		t.Fatalf("expected exactly 1 attempt before cancellation, got %d", repo.calls)
	}
}

// TestNewEmployeeLimitReplicaConsumer_ConstructsAndCloses mirrors the client
// consumer lifecycle coverage for the employee-limit consumer.
func TestNewEmployeeLimitReplicaConsumer_ConstructsAndCloses(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := NewEmployeeLimitReplicaConsumer("localhost:9999", repo)
	if c == nil || c.reader == nil {
		t.Fatal("expected a constructed consumer with a reader")
	}
	c.Close()
}

func TestEmployeeLimitConsumer_Start_ExitsOnCancelledContext(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := NewEmployeeLimitReplicaConsumer("localhost:9999", repo)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	if repo.calls != 0 {
		t.Fatalf("no messages should have been handled, got %d calls", repo.calls)
	}
}

func TestEmployeeLimitHandleWithRetry_ExhaustsRetries(t *testing.T) {
	repo := &fakeLimitReplicaRepo{err: context.DeadlineExceeded, failCount: 99}
	c := &EmployeeLimitReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	payload, _ := json.Marshal(kafkamsg.EmployeeLimitsUpdatedMessage{EmployeeID: 1, Version: 1})

	if err := c.handleWithRetry(context.Background(), payload); err == nil {
		t.Fatal("expected the last error after exhausting all attempts")
	}
	if repo.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", repo.calls)
	}
}

func TestEmployeeLimitHandleWithRetry_CancelDuringBackoff(t *testing.T) {
	repo := &fakeLimitReplicaRepo{err: context.DeadlineExceeded, failCount: 99}
	c := &EmployeeLimitReplicaConsumer{repo: repo, backoff: []time.Duration{time.Hour}}
	payload, _ := json.Marshal(kafkamsg.EmployeeLimitsUpdatedMessage{EmployeeID: 1, Version: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.handleWithRetry(ctx, payload); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if repo.calls != 1 {
		t.Fatalf("expected exactly 1 attempt before cancellation, got %d", repo.calls)
	}
}
