package handler_test

import (
	"context"
	"errors"
	"testing"

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/stock-service/internal/handler"
	"github.com/exbanka/stock-service/internal/model"
	"google.golang.org/grpc"
)

// stubClientLookup is a test double for ClientServiceLookup.
type stubClientLookup struct {
	called bool
	resp   *clientpb.ClientResponse
	err    error
}

func (s *stubClientLookup) GetClient(_ context.Context, in *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	s.called = true
	return s.resp, s.err
}

// stubReplicaStore is a test double for the clientReplicaStore interface.
type stubReplicaStore struct {
	data     map[uint64]model.ClientReplica
	upserted []model.ClientReplica
}

func newStubReplica(ids ...uint64) *stubReplicaStore {
	s := &stubReplicaStore{data: make(map[uint64]model.ClientReplica)}
	for _, id := range ids {
		s.data[id] = model.ClientReplica{ID: id, Email: "x@y.com"}
	}
	return s
}

func (s *stubReplicaStore) GetByID(_ context.Context, id uint64) (model.ClientReplica, error) {
	if r, ok := s.data[id]; ok {
		return r, nil
	}
	return model.ClientReplica{}, errors.New("not found")
}

func (s *stubReplicaStore) Upsert(_ context.Context, in model.ClientReplica) error {
	s.upserted = append(s.upserted, in)
	return nil
}

// TestClientSellerValidator_ReplicaHit_NoGRPC verifies that when the replica
// has a row for the client id, SellerExists returns true WITHOUT calling GetClient.
func TestClientSellerValidator_ReplicaHit_NoGRPC(t *testing.T) {
	grpcStub := &stubClientLookup{}
	replicaStub := newStubReplica(42)

	v := handler.NewClientSellerValidator(grpcStub, replicaStub)
	if !v.SellerExists(context.Background(), "client-42") {
		t.Fatal("expected true for client known in replica")
	}
	if grpcStub.called {
		t.Error("GetClient must NOT be called when the replica already has the client")
	}
}

// TestClientSellerValidator_ReplicaMiss_FallsBackToGRPC verifies that when the
// replica has no row for the client, SellerExists falls back to gRPC, returns
// true on success, and backfills the replica at Version 0.
func TestClientSellerValidator_ReplicaMiss_FallsBackToGRPC(t *testing.T) {
	grpcStub := &stubClientLookup{
		resp: &clientpb.ClientResponse{
			Id: 7, Email: "a@b.com", FirstName: "Ana", LastName: "Banic", Jmbg: "1234567890123",
		},
	}
	replicaStub := newStubReplica() // empty — no clients in replica

	v := handler.NewClientSellerValidator(grpcStub, replicaStub)
	if !v.SellerExists(context.Background(), "client-7") {
		t.Fatal("expected true for client returned by gRPC")
	}
	if !grpcStub.called {
		t.Error("GetClient must be called on replica miss")
	}
	if len(replicaStub.upserted) != 1 {
		t.Fatalf("expected 1 backfill upsert, got %d", len(replicaStub.upserted))
	}
	u := replicaStub.upserted[0]
	if u.ID != 7 || u.Email != "a@b.com" || u.Version != 0 {
		t.Errorf("unexpected backfill row: %+v", u)
	}
}

// TestClientSellerValidator_NilReplica_PureGRPC verifies that when no replica
// is wired (nil), SellerExists behaves as pure-gRPC fallback (legacy mode).
func TestClientSellerValidator_NilReplica_PureGRPC(t *testing.T) {
	grpcStub := &stubClientLookup{
		resp: &clientpb.ClientResponse{Id: 5},
	}

	v := handler.NewClientSellerValidator(grpcStub, nil)
	if !v.SellerExists(context.Background(), "client-5") {
		t.Fatal("expected true with pure-gRPC path")
	}
	if !grpcStub.called {
		t.Error("GetClient must be called when no replica is wired")
	}
}

// TestClientSellerValidator_GRPCError_ReturnsFalse verifies that a gRPC error
// causes SellerExists to return false (fail closed).
func TestClientSellerValidator_GRPCError_ReturnsFalse(t *testing.T) {
	grpcStub := &stubClientLookup{err: errors.New("unavailable")}
	replicaStub := newStubReplica() // empty

	v := handler.NewClientSellerValidator(grpcStub, replicaStub)
	if v.SellerExists(context.Background(), "client-99") {
		t.Fatal("expected false when gRPC returns error")
	}
}

// TestClientSellerValidator_NonClientID_AlwaysTrue verifies that bank/employee-*
// ids bypass both the replica and gRPC checks.
func TestClientSellerValidator_NonClientID_AlwaysTrue(t *testing.T) {
	grpcStub := &stubClientLookup{}
	replicaStub := newStubReplica()

	v := handler.NewClientSellerValidator(grpcStub, replicaStub)
	for _, id := range []string{"bank", "employee-1", "employee-99"} {
		if !v.SellerExists(context.Background(), id) {
			t.Errorf("expected true for non-client id %q", id)
		}
	}
	if grpcStub.called {
		t.Error("GetClient must not be called for non-client ids")
	}
	if len(replicaStub.upserted) != 0 {
		t.Error("replica must not be touched for non-client ids")
	}
}
