package handler

import (
	"context"

	"github.com/exbanka/contract/identity"
)

// ownsLoan reports whether the gRPC caller (from identity metadata) may access a
// loan/loan-request/installment set owned by clientID. OWN-1: client → own only;
// employee-on-behalf → bound client; employee/admin + trusted service → allowed
// (the gateway already permission-gated employees). A false result is mapped by
// callers to a NotFound sentinel (don't leak existence across tenants) or, for
// list-by-client, to ErrForbidden.
func ownsLoan(ctx context.Context, clientID uint64) bool {
	return identity.FromIncoming(ctx).OwnsResource(int64(clientID))
}
