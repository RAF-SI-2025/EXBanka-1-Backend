// Package identity propagates the authenticated caller's identity from the
// api-gateway to backend services over gRPC metadata, so each owning service can
// enforce ownership of its own resources (the "OWN-1" model) instead of relying
// on the gateway.
//
// It is intentionally separate from contract/changelog (which carries only the
// audit attribution key x-changed-by). Both can be present on the same call.
package identity

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"
)

// Metadata keys carrying the caller identity.
const (
	mdPrincipalType    = "x-principal-type"
	mdPrincipalID      = "x-principal-id"
	mdOnBehalfClientID = "x-on-behalf-client-id"
)

// Principal type values.
const (
	PrincipalEmployee = "employee"
	PrincipalClient   = "client"
	// PrincipalService marks an internal service-to-service call (no human
	// caller). Absent identity is also treated as a service call for backward
	// compatibility with callers that don't yet stamp identity.
	PrincipalService = "service"
)

// Caller is the resolved identity of whoever is making the request.
type Caller struct {
	// PrincipalType is "employee", "client", "service", or "" (== service).
	PrincipalType string
	// PrincipalID is the principal's primary-key id (0 for service / unknown).
	PrincipalID int64
	// OnBehalfClientID is the client an employee is acting for (0 if none).
	OnBehalfClientID int64
}

// IsClient reports whether the caller is an end-client (the only principal type
// subject to cross-tenant data-ownership checks).
func (c Caller) IsClient() bool { return c.PrincipalType == PrincipalClient }

// IsEmployee reports whether the caller is a bank employee.
func (c Caller) IsEmployee() bool { return c.PrincipalType == PrincipalEmployee }

// IsService reports whether the call is internal service-to-service. Absent
// identity (legacy / untagged callers) counts as a trusted service call so
// existing money-path RPCs (UpdateBalance, Reserve*/Settle*/…) keep working.
func (c Caller) IsService() bool {
	return c.PrincipalType == PrincipalService || c.PrincipalType == ""
}

// OwnsResource is the OWN-1 data-ownership primitive every owning service uses to
// decide whether this caller may access a resource owned by ownerID:
//   - client            → only their own resources (PrincipalID == ownerID)
//   - employee on-behalf → only the bound client's resources (OnBehalfClientID == ownerID)
//   - employee (admin)   → allowed (route-level RBAC is enforced by the gateway)
//   - service            → allowed (trusted internal call)
//
// Callers map a false result to their own NotFound sentinel (existence must not
// leak across tenants — 404, not 403).
func (c Caller) OwnsResource(ownerID int64) bool {
	switch {
	case c.IsClient():
		return c.PrincipalID == ownerID
	case c.IsEmployee() && c.OnBehalfClientID != 0:
		return c.OnBehalfClientID == ownerID
	default:
		return true
	}
}

// Inject returns a context that carries the caller identity as OUTGOING gRPC
// metadata. Safe to call alongside changelog.SetChangedBy — metadata merges.
// A zero/service Caller injects nothing (keeps the wire clean).
func Inject(ctx context.Context, c Caller) context.Context {
	if c.PrincipalType == "" || c.PrincipalType == PrincipalService {
		return ctx
	}
	kv := []string{mdPrincipalType, c.PrincipalType}
	if c.PrincipalID != 0 {
		kv = append(kv, mdPrincipalID, strconv.FormatInt(c.PrincipalID, 10))
	}
	if c.OnBehalfClientID != 0 {
		kv = append(kv, mdOnBehalfClientID, strconv.FormatInt(c.OnBehalfClientID, 10))
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

// FromIncoming extracts the Caller from incoming gRPC metadata on the service
// side. Absent identity yields a zero Caller (IsService()==true).
func FromIncoming(ctx context.Context) Caller {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Caller{}
	}
	return Caller{
		PrincipalType:    first(md, mdPrincipalType),
		PrincipalID:      firstInt(md, mdPrincipalID),
		OnBehalfClientID: firstInt(md, mdOnBehalfClientID),
	}
}

func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func firstInt(md metadata.MD, key string) int64 {
	s := first(md, key)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
