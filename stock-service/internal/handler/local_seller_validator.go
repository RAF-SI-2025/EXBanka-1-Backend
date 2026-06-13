package handler

import (
	"context"
	"log"
	"strconv"
	"strings"

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/stock-service/internal/model"
	"google.golang.org/grpc"
)

// ClientServiceLookup is the subset of clientpb.ClientServiceClient the seller
// validator uses. Declared as an interface so tests can stub it without a gRPC
// connection; the real clientpb.ClientServiceClient satisfies it.
type ClientServiceLookup interface {
	GetClient(ctx context.Context, in *clientpb.GetClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error)
}

// clientReplicaStore is the subset of ClientReplicaRepository the validator
// uses: read-first existence check and best-effort backfill on gRPC hit.
type clientReplicaStore interface {
	GetByID(ctx context.Context, id uint64) (model.ClientReplica, error)
	Upsert(ctx context.Context, in model.ClientReplica) error
}

// ClientSellerValidator is the production LocalSellerValidator: it confirms a
// client-<n> participant id resolves to a real client on this bank. The
// replica store (SP-1) is consulted first; a cache miss falls back to
// client-service GetClient and backfills the replica so future calls are
// served locally.
type ClientSellerValidator struct {
	clients ClientServiceLookup
	replica clientReplicaStore // nil when no replica is wired (pure-gRPC mode)
}

// NewClientSellerValidator wires a ClientSellerValidator against a
// client-service client and an optional local client_replica store. When
// replica is nil the validator always uses gRPC (legacy behaviour).
func NewClientSellerValidator(clients ClientServiceLookup, replica clientReplicaStore) *ClientSellerValidator {
	return &ClientSellerValidator{clients: clients, replica: replica}
}

// SellerExists reports whether participantID (a "client-<n>" id) resolves to a
// real client on this bank.
//
// Fast path: if a local replica store is wired and holds a row for id, return
// true immediately without a gRPC call.
//
// Slow path: on a replica miss (or no replica), fall back to GetClient. On a
// successful gRPC response, backfill the replica at Version 0 — any future
// client.created/client.updated event (version > 0) will supersede it via the
// version-guarded Upsert.
//
// Fail closed: a parse failure, a gRPC error, or a nil/mismatched response is
// treated as non-existent. A non-client id (bank, employee-*) returns true
// immediately — the handler validates those forms structurally.
func (v *ClientSellerValidator) SellerExists(ctx context.Context, participantID string) bool {
	rest, ok := strings.CutPrefix(participantID, "client-")
	if !ok {
		return true // not a client id — handler validates bank/employee structurally
	}
	id, err := strconv.ParseUint(rest, 10, 64)
	if err != nil || id == 0 {
		return false
	}

	// Fast path: local replica hit.
	if v.replica != nil {
		if _, err := v.replica.GetByID(ctx, id); err == nil {
			return true
		}
	}

	// Slow path: gRPC fallback.
	resp, gerr := v.clients.GetClient(ctx, &clientpb.GetClientRequest{Id: id})
	if gerr != nil || resp == nil {
		return false
	}
	if resp.GetId() != id {
		return false
	}

	// Backfill replica at Version 0; any incoming Kafka event (version > 0)
	// will supersede it via the version-guarded Upsert.
	if v.replica != nil {
		if err := v.replica.Upsert(ctx, model.ClientReplica{
			ID:        id,
			Email:     resp.GetEmail(),
			FirstName: resp.GetFirstName(),
			LastName:  resp.GetLastName(),
			JMBG:      resp.GetJmbg(),
			Version:   0,
		}); err != nil {
			log.Printf("client-seller-validator: replica backfill id=%d: %v", id, err)
		}
	}

	return true
}
