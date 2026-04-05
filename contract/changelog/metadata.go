// contract/changelog/metadata.go
package changelog

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"
)

const metadataKeyChangedBy = "x-changed-by"

// SetChangedBy returns a new outgoing context with the x-changed-by metadata.
// Call this in API gateway handlers before making gRPC calls.
func SetChangedBy(ctx context.Context, userID int64) context.Context {
	md := metadata.Pairs(metadataKeyChangedBy, strconv.FormatInt(userID, 10))
	return metadata.NewOutgoingContext(ctx, md)
}

// ExtractChangedBy reads the x-changed-by value from incoming gRPC metadata.
// Returns 0 if not present (system-initiated change).
func ExtractChangedBy(ctx context.Context) int64 {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0
	}
	vals := md.Get(metadataKeyChangedBy)
	if len(vals) == 0 {
		return 0
	}
	id, err := strconv.ParseInt(vals[0], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
