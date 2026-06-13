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

// errMockReplicaTransient is a stand-in transient error for retry tests.
var errMockReplicaTransient = errors.New("transient db error")

type fakeReplicaRepo struct {
	last      model.ClientReplica
	calls     int
	err       error // returned on every call while calls <= failCount
	failCount int   // fail the first N calls then succeed
}

func (f *fakeReplicaRepo) Upsert(_ context.Context, in model.ClientReplica) error {
	f.calls++
	if f.err != nil && f.calls <= f.failCount {
		return f.err
	}
	f.last = in
	return nil
}

func marshalReplicaEvent(t *testing.T, evt kafkamsg.ClientCreatedMessage) []byte {
	t.Helper()
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestHandleClientReplicaEvent_UpsertsReplica(t *testing.T) {
	repo := &fakeReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo, backoff: defaultBackoff}
	payload := marshalReplicaEvent(t, kafkamsg.ClientCreatedMessage{
		ClientID: 42, Email: "x@y.com", FirstName: "X", LastName: "Y", JMBG: "9999999999999", Version: 3,
	})
	if err := c.handleReplica(context.Background(), payload); err != nil {
		t.Fatalf("handleReplica: %v", err)
	}
	if repo.calls != 1 || repo.last.ID != 42 || repo.last.Email != "x@y.com" || repo.last.JMBG != "9999999999999" || repo.last.Version != 3 {
		t.Fatalf("bad upsert: %+v calls=%d", repo.last, repo.calls)
	}
}

func TestHandleClientReplicaEvent_BadJSON(t *testing.T) {
	repo := &fakeReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo, backoff: defaultBackoff}
	err := c.handleReplica(context.Background(), []byte("{not json"))
	if err == nil {
		t.Fatalf("expected error on malformed json")
	}
	if !errors.Is(err, errMalformed) {
		t.Fatalf("expected errMalformed, got %v", err)
	}
	if repo.calls != 0 {
		t.Fatalf("repo should not be called on bad json, got %d calls", repo.calls)
	}
}

func TestHandleReplicaWithRetry_RetriesTransientError(t *testing.T) {
	repo := &fakeReplicaRepo{
		err:       errMockReplicaTransient,
		failCount: 2, // fail first 2 calls, succeed on 3rd
	}
	payload := marshalReplicaEvent(t, kafkamsg.ClientCreatedMessage{
		ClientID: 7, Email: "a@b.com", FirstName: "A", LastName: "B", JMBG: "1234567890123", Version: 1,
	})
	// Zero-duration backoff so the test runs instantly.
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	if err := c.handleReplicaWithRetry(context.Background(), payload); err != nil {
		t.Fatalf("expected nil after retries succeeded, got: %v", err)
	}
	if repo.calls != 3 {
		t.Fatalf("expected 3 calls (2 failures + 1 success), got %d", repo.calls)
	}
}

func TestHandleReplicaWithRetry_MalformedNotRetried(t *testing.T) {
	repo := &fakeReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	err := c.handleReplicaWithRetry(context.Background(), []byte("{bad"))
	if err == nil {
		t.Fatalf("expected error for malformed payload")
	}
	if repo.calls != 0 {
		t.Fatalf("repo should not be called for malformed payload, got %d calls", repo.calls)
	}
}
