package service

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
)

// fakeClientReplicaReader is a test double for clientReplicaReader.
type fakeClientReplicaReader struct {
	replica     model.ClientReplica
	getErr      error
	upsertErr   error
	upsertLast  model.ClientReplica
	upsertCalls int
}

func (f *fakeClientReplicaReader) GetByID(_ context.Context, id uint64) (model.ClientReplica, error) {
	if f.getErr != nil {
		return model.ClientReplica{}, f.getErr
	}
	return f.replica, nil
}

func (f *fakeClientReplicaReader) Upsert(_ context.Context, in model.ClientReplica) error {
	f.upsertCalls++
	f.upsertLast = in
	return f.upsertErr
}

// stubGetClientClient embeds clientpb.ClientServiceClient and overrides only
// GetClient — the only method CronService.resolveClientEmail calls.
type stubGetClientClient struct {
	clientpb.ClientServiceClient // satisfy all other methods via embedding
	response                     *clientpb.ClientResponse
	err                          error
	calls                        int
}

func (s *stubGetClientClient) GetClient(_ context.Context, _ *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

// TestResolveClientEmail_ReplicaHit verifies that when the replica contains
// the client's profile, resolveClientEmail returns the email without calling gRPC.
func TestResolveClientEmail_ReplicaHit(t *testing.T) {
	replica := &fakeClientReplicaReader{
		replica: model.ClientReplica{ID: 42, Email: "hit@example.com"},
	}
	cron := &CronService{clientReplicaRepo: replica, clientClient: nil}

	email := cron.resolveClientEmail(context.Background(), 42)
	if email != "hit@example.com" {
		t.Fatalf("expected replica email, got %q", email)
	}
	if replica.upsertCalls != 0 {
		t.Fatalf("upsert must not be called on replica hit, got %d calls", replica.upsertCalls)
	}
}

// TestResolveClientEmail_NilRepoNilClient returns "" gracefully.
func TestResolveClientEmail_NilRepoNilClient(t *testing.T) {
	cron := &CronService{clientReplicaRepo: nil, clientClient: nil}
	email := cron.resolveClientEmail(context.Background(), 1)
	if email != "" {
		t.Fatalf("expected empty string, got %q", email)
	}
}

// TestResolveClientEmail_ReplicaMiss_NilClientClient returns "" without panic.
func TestResolveClientEmail_ReplicaMiss_NilClientClient(t *testing.T) {
	replica := &fakeClientReplicaReader{
		getErr: errors.New("not found"),
	}
	cron := &CronService{clientReplicaRepo: replica, clientClient: nil}
	email := cron.resolveClientEmail(context.Background(), 99)
	if email != "" {
		t.Fatalf("expected empty string when gRPC client is nil, got %q", email)
	}
}

// TestResolveClientEmail_ReplicaMiss_FallbackAndBackfill verifies the SP-1
// hybrid lazy-fallback path: replica miss → single GetClient gRPC call →
// backfill replica at Version 0 → return live email.
func TestResolveClientEmail_ReplicaMiss_FallbackAndBackfill(t *testing.T) {
	const clientID = uint64(7)
	replica := &fakeClientReplicaReader{
		getErr: repository.ErrClientReplicaNotFound,
	}
	stub := &stubGetClientClient{
		response: &clientpb.ClientResponse{
			Id:        clientID,
			Email:     "live@x.com",
			FirstName: "L",
			LastName:  "C",
			Jmbg:      "123",
		},
	}
	cron := &CronService{clientReplicaRepo: replica, clientClient: stub}

	email := cron.resolveClientEmail(context.Background(), clientID)

	if email != "live@x.com" {
		t.Fatalf("expected live@x.com, got %q", email)
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly 1 gRPC GetClient call, got %d", stub.calls)
	}
	if replica.upsertCalls != 1 {
		t.Fatalf("expected exactly 1 replica Upsert (backfill), got %d", replica.upsertCalls)
	}
	if replica.upsertLast.Email != "live@x.com" {
		t.Fatalf("backfilled email mismatch: got %q", replica.upsertLast.Email)
	}
}
