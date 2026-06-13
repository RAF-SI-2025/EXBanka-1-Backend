package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/exbanka/account-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// TestHandleWithRetry_ExhaustsAllAttempts drives the limit consumer's retry loop
// to exhaustion: a transient apply error on every attempt must return the last
// error after len(backoff)+1 tries.
func TestHandleWithRetry_ExhaustsAllAttempts(t *testing.T) {
	repo := &fakePolicyRepo{}
	applier := &fakeApplier{err: context.DeadlineExceeded, failCount: 100} // never succeeds
	c := &ClientLimitConsumer{repo: repo, applier: applier, backoff: []time.Duration{0}}

	payload := marshalEvent(t, kafkamsg.ClientLimitsUpdatedMessage{
		ClientID: 99, DailyLimit: "1.0000", Version: 1,
	})

	err := c.handleWithRetry(context.Background(), payload)
	if err == nil {
		t.Fatal("expected the exhausted retry loop to return the last error")
	}
	if applier.calls != 2 { // len(backoff)+1 = 2 attempts
		t.Fatalf("expected 2 attempts, got %d", applier.calls)
	}
}

// erroringReplicaRepo always fails Upsert with a transient error.
type erroringReplicaRepo struct{}

func (erroringReplicaRepo) Upsert(context.Context, model.ClientReplica) error {
	return errors.New("transient upsert failure")
}

// TestHandleReplicaWithRetry_ExhaustsAllAttempts is the replica-consumer
// counterpart: a persistent upsert error exhausts the retry loop.
func TestHandleReplicaWithRetry_ExhaustsAllAttempts(t *testing.T) {
	c := &ClientReplicaConsumer{repo: erroringReplicaRepo{}, backoff: []time.Duration{0}}

	payload, err := json.Marshal(kafkamsg.ClientCreatedMessage{
		ClientID: 7, Email: "x@y.z", FirstName: "A", LastName: "B", JMBG: "1234567890123", Version: 1,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := c.handleReplicaWithRetry(context.Background(), payload); err == nil {
		t.Fatal("expected the exhausted retry loop to return the last error")
	}
}

// TestHandleReplica_BadJSON covers the malformed-payload (no-retry) branch.
func TestHandleReplica_BadJSON(t *testing.T) {
	c := &ClientReplicaConsumer{repo: noopReplicaRepo{}, backoff: defaultBackoff}
	err := c.handleReplicaWithRetry(context.Background(), []byte("{not json"))
	if err == nil || !errors.Is(err, errMalformed) {
		t.Fatalf("expected errMalformed for bad json, got %v", err)
	}
}
