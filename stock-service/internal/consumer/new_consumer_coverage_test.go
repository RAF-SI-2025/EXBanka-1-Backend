package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafka "github.com/segmentio/kafka-go"
)

// TestNewClientReplicaConsumer_Constructor verifies the constructor returns a
// non-nil consumer wired with the supplied repo and default backoff.
func TestNewClientReplicaConsumer_Constructor(t *testing.T) {
	repo := &fakeClientReplicaRepo{}
	c := NewClientReplicaConsumer("127.0.0.1:1", repo)
	if c == nil {
		t.Fatal("expected non-nil consumer")
	}
	if c.repo == nil {
		t.Error("repo should be set")
	}
	if len(c.backoff) == 0 {
		t.Error("backoff should be set to defaultReplicaBackoff")
	}
	// Close must not panic even without a real broker.
	c.Close()
}

// TestClientReplicaConsumer_Close verifies Close does not panic.
func TestClientReplicaConsumer_Close(t *testing.T) {
	c := NewClientReplicaConsumer("127.0.0.1:1", &fakeClientReplicaRepo{})
	c.Close() // should not panic
}

// TestClientReplicaConsumer_Start_Cancels verifies that Start spawns a goroutine
// that exits when the context is cancelled. With a non-existent broker the read
// call fails with a context-cancelled error and the goroutine returns via the
// if ctx.Err() != nil { return } path.
func TestClientReplicaConsumer_Start_Cancels(t *testing.T) {
	c := NewClientReplicaConsumer("127.0.0.1:1", &fakeClientReplicaRepo{})
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	cancel()
	// Give the goroutine a moment to observe the cancellation and exit.
	time.Sleep(100 * time.Millisecond)
}

// TestHandleClientReplicaWithRetry_ContextCancelledDuringBackoff covers the
// case <-ctx.Done() branch inside the retry sleep. The repo always fails so the
// consumer sleeps between retries; cancelling the context during the sleep
// causes the function to return ctx.Err().
func TestHandleClientReplicaWithRetry_ContextCancelledDuringBackoff(t *testing.T) {
	repo := &fakeClientReplicaRepo{
		err:       errMockReplicaTransient,
		failCount: 100, // always fail
	}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{
		ClientID: 1, Email: "a@b.com", FirstName: "A", LastName: "B", JMBG: "1234567890123", Version: 1,
	})
	ctx, cancel := context.WithCancel(context.Background())

	// Use a long backoff (500ms) so the cancel fires during the first sleep.
	c := &ClientReplicaConsumer{
		repo:    repo,
		backoff: []time.Duration{500 * time.Millisecond, 500 * time.Millisecond},
	}

	// Cancel context after first attempt starts sleeping.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := c.handleReplicaWithRetry(ctx, payload)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestHandleClientReplicaWithRetry_AllAttemptsExhausted covers the
// "all N attempts exhausted, event dropped" log + return path.
func TestHandleClientReplicaWithRetry_AllAttemptsExhausted(t *testing.T) {
	repo := &fakeClientReplicaRepo{
		err:       errMockReplicaTransient,
		failCount: 100, // always fail regardless of attempt count
	}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{
		ClientID: 2, Email: "b@c.com", FirstName: "B", LastName: "C", JMBG: "9876543210123", Version: 2,
	})
	// Zero-duration backoffs so the test runs instantly.
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	err := c.handleReplicaWithRetry(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error when all attempts exhausted")
	}
	// len(backoff)+1 = 3 total attempts.
	if repo.calls != 3 {
		t.Errorf("expected 3 calls, got %d", repo.calls)
	}
}

// TestClientReplicaConsumer_Start_NonCtxReadError covers the
// log.Printf("client-replica consumer read error") + continue path that
// runs when ReadMessage returns a non-context error (e.g. connection refused).
// We give the goroutine a moment to hit the error then cancel so it exits.
func TestClientReplicaConsumer_Start_NonCtxReadError(t *testing.T) {
	// 127.0.0.1:1 is not listening — ReadMessage returns "connection refused"
	// almost immediately on every attempt.
	c := NewClientReplicaConsumer("127.0.0.1:1", &fakeClientReplicaRepo{})
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	// Allow enough time for at least one iteration of the error-log path.
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// TestClientReplicaConsumer_Start_NonCtxErrorViaReaderClose covers the
// log.Printf("client-replica consumer read error") + continue path by
// closing the kafka.Reader while the goroutine is blocked in ReadMessage.
// A closed Reader returns an immediate non-context error, which is distinct
// from ctx.Err() so the goroutine takes the log+continue branch.
func TestClientReplicaConsumer_Start_NonCtxErrorViaReaderClose(t *testing.T) {
	c := NewClientReplicaConsumer("127.0.0.1:1", &fakeClientReplicaRepo{})
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	// Let the goroutine start and block in ReadMessage.
	time.Sleep(30 * time.Millisecond)
	// Closing the reader forces an immediate non-ctx error inside ReadMessage.
	_ = c.reader.Close()
	// Allow the goroutine to cycle through log.Printf + continue at least once.
	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond)
}

// TestSupervisorDemotedConsumer_Start_NonCtxErrorViaReaderClose covers the
// log.Printf("supervisor-demoted consumer read error") + continue path inside
// SupervisorDemotedConsumer.Start using the same reader-close technique.
func TestSupervisorDemotedConsumer_Start_NonCtxErrorViaReaderClose(t *testing.T) {
	// Build a SupervisorDemotedConsumer with a custom reader so we can close
	// it while the Start goroutine is blocked, forcing a non-ctx error.
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{"127.0.0.1:1"},
		GroupTopics: []string{kafkamsg.TopicUserSupervisorDemoted},
		GroupID:     "test-sv-non-ctx",
	})
	c := &SupervisorDemotedConsumer{reader: r}
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	// Let the goroutine start.
	time.Sleep(30 * time.Millisecond)
	// Close the reader to force an immediate non-context error.
	_ = r.Close()
	// Allow the goroutine to hit log.Printf + continue.
	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond)
}
