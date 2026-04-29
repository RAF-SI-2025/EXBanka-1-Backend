package grpc

import (
	transactionpb "github.com/exbanka/contract/transactionpb"
	"google.golang.org/grpc"
)

// NewPeerTxServiceClient connects to the PeerTxService RPCs hosted on
// transaction-service. Used by the gateway's POST /api/v3/interbank
// handler to dispatch decoded SI-TX envelopes.
func NewPeerTxServiceClient(addr string) (transactionpb.PeerTxServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewPeerTxServiceClient(conn), conn, nil
}
