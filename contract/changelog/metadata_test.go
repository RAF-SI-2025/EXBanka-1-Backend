package changelog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestSetAndExtractChangedBy(t *testing.T) {
	ctx := context.Background()
	outCtx := SetChangedBy(ctx, 42)

	// Simulate what gRPC does: outgoing metadata becomes incoming metadata
	md, _ := metadata.FromOutgoingContext(outCtx)
	inCtx := metadata.NewIncomingContext(ctx, md)

	got := ExtractChangedBy(inCtx)
	assert.Equal(t, int64(42), got)
}

func TestExtractChangedBy_Missing(t *testing.T) {
	ctx := context.Background()
	got := ExtractChangedBy(ctx)
	assert.Equal(t, int64(0), got)
}
