package grpc

import (
	transactionpb "github.com/exbanka/contract/transactionpb"
	"google.golang.org/grpc"
)

// NewPeerUserServiceClient connects to interbank-service's PeerUserService,
// which resolves a local user (client-N / employee-N) to display names for the
// peer-facing GET /cross-bank-protocol/user/{rid}/{id} endpoint (SI-TX §9).
func NewPeerUserServiceClient(addr string) (transactionpb.PeerUserServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewPeerUserServiceClient(conn), conn, nil
}
