package grpc_client

import (
	"context"
	"errors"
	"testing"

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// fakeClientLimitClient implements clientpb.ClientLimitServiceClient and
// records the request it received from the adapter.
type fakeClientLimitClient struct {
	clientpb.ClientLimitServiceClient

	gotReq *clientpb.SetClientLimitRequest
	err    error
}

func (f *fakeClientLimitClient) SetClientLimits(_ context.Context, in *clientpb.SetClientLimitRequest, _ ...grpc.CallOption) (*clientpb.ClientLimitResponse, error) {
	f.gotReq = in
	if f.err != nil {
		return nil, f.err
	}
	return &clientpb.ClientLimitResponse{}, nil
}

func TestClientLimitAdapter_SetClientLimits_PassesArgs(t *testing.T) {
	fc := &fakeClientLimitClient{}
	a := NewClientLimitAdapter(fc)
	require.NotNil(t, a)

	err := a.SetClientLimits(context.Background(), 42, "100", "200", "50", 9)
	require.NoError(t, err)
	require.NotNil(t, fc.gotReq)
	assert.Equal(t, int64(42), fc.gotReq.ClientId)
	assert.Equal(t, "100", fc.gotReq.DailyLimit)
	assert.Equal(t, "200", fc.gotReq.MonthlyLimit)
	assert.Equal(t, "50", fc.gotReq.TransferLimit)
	assert.Equal(t, int64(9), fc.gotReq.SetByEmployee)
}

func TestClientLimitAdapter_SetClientLimits_PropagatesError(t *testing.T) {
	fc := &fakeClientLimitClient{err: errors.New("rpc fail")}
	a := NewClientLimitAdapter(fc)

	err := a.SetClientLimits(context.Background(), 1, "1", "1", "1", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rpc fail")
}
