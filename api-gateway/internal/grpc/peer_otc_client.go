package grpc

import (
	stockpb "github.com/exbanka/contract/stockpb"
	"google.golang.org/grpc"
)

// NewPeerOTCServiceClient connects to the PeerOTCService RPCs hosted on
// stock-service. Used by the gateway's /api/v3/public-stock and
// /api/v3/negotiations/* handlers.
func NewPeerOTCServiceClient(addr string) (stockpb.PeerOTCServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewPeerOTCServiceClient(conn), conn, nil
}
