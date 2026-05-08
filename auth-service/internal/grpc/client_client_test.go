package grpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewClientServiceClient_BuildsLazyConn validates that the constructor
// returns a working *ClientConn without dialing (gRPC does lazy connect).
// We then close the conn to release resources.
func TestNewClientServiceClient_BuildsLazyConn(t *testing.T) {
	client, conn, err := NewClientServiceClient("127.0.0.1:1")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NotNil(t, client)
	t.Cleanup(func() {
		assert.NoError(t, conn.Close())
	})
}
