package handler

import (
	"context"

	"github.com/exbanka/contract/identity"
)

// ownsTxn reports whether the gRPC caller (from identity metadata) may access a
// payment/transfer owned by clientID. OWN-1: client → own only; employee-on-
// behalf → bound client; employee/admin + trusted service → allowed (the gateway
// already permission-gated employees). Callers map a false result to a NotFound
// sentinel (don't leak existence across tenants).
func ownsTxn(ctx context.Context, clientID uint64) bool {
	return identity.FromIncoming(ctx).OwnsResource(int64(clientID))
}
