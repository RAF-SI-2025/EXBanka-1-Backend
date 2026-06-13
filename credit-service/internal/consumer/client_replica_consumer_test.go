package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/credit-service/internal/model"
)

// errMockClientTransient is a stand-in transient error for retry tests.
var errMockClientTransient = errors.New("transient db error")

type fakeClientReplicaRepo struct {
	last      model.ClientReplica
	calls     int
	err       error // returned on every call while calls < failCount
	failCount int   // fail the first N calls then succeed
}

func (f *fakeClientReplicaRepo) Upsert(_ context.Context, in model.ClientReplica) error {
	f.calls++
	if f.err != nil && f.calls <= f.failCount {
		return f.err
	}
	f.last = in
	return nil
}

func TestHandleClientReplicaEvent_UpsertsReplica(t *testing.T) {
	repo := &fakeClientReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo, backoff: defaultBackoff}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{
		ClientID: 42, Email: "x@y.com", FirstName: "X", LastName: "Y", JMBG: "9999999999999", Version: 3,
	})
	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if repo.calls != 1 || repo.last.ID != 42 || repo.last.Email != "x@y.com" || repo.last.JMBG != "9999999999999" || repo.last.Version != 3 {
		t.Fatalf("bad upsert: %+v calls=%d", repo.last, repo.calls)
	}
}

func TestHandleClientReplicaEvent_BadJSON(t *testing.T) {
	repo := &fakeClientReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo, backoff: defaultBackoff}
	if err := c.handle(context.Background(), []byte("{not json")); err == nil {
		t.Fatalf("expected error on malformed json")
	}
	if repo.calls != 0 {
		t.Fatalf("repo should not be called on bad json, got %d calls", repo.calls)
	}
}

func TestClientReplicaHandleWithRetry_RetriesTransientError(t *testing.T) {
	repo := &fakeClientReplicaRepo{
		err:       errMockClientTransient,
		failCount: 2, // fail first 2 calls, succeed on 3rd
	}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{
		ClientID: 7, Email: "a@b.com", FirstName: "A", LastName: "B", JMBG: "1234567890123", Version: 1,
	})
	// Zero-duration backoff so the test runs instantly.
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	if err := c.handleWithRetry(context.Background(), payload); err != nil {
		t.Fatalf("expected nil after retries succeeded, got: %v", err)
	}
	if repo.calls != 3 {
		t.Fatalf("expected 3 calls (2 failures + 1 success), got %d", repo.calls)
	}
}

func TestClientReplicaHandleWithRetry_MalformedNotRetried(t *testing.T) {
	repo := &fakeClientReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	err := c.handleWithRetry(context.Background(), []byte("{bad"))
	if err == nil {
		t.Fatalf("expected error for malformed payload")
	}
	if repo.calls != 0 {
		t.Fatalf("repo should not be called for malformed payload, got %d calls", repo.calls)
	}
}
