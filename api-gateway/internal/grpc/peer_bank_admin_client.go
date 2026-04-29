package grpc

import (
	transactionpb "github.com/exbanka/contract/transactionpb"
	"google.golang.org/grpc"
)

// NewPeerBankAdminServiceClient connects to the PeerBankAdminService RPCs
// hosted on transaction-service. Used by the gateway's /api/v3/peer-banks
// admin routes (gated by peer_banks.manage.any).
func NewPeerBankAdminServiceClient(addr string) (transactionpb.PeerBankAdminServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewPeerBankAdminServiceClient(conn), conn, nil
}
