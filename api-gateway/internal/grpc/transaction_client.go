package grpc

import (
	"google.golang.org/grpc"

	"github.com/exbanka/contract/shared"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

func NewTransactionClient(addr string) (transactionpb.TransactionServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewTransactionServiceClient(conn), conn, nil
}

func NewFeeServiceClient(addr string) (transactionpb.FeeServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewFeeServiceClient(conn), conn, nil
}

// NewInterBankServiceClient connects to the InterBankService RPCs hosted on
// transaction-service. Used by the gateway's HMAC-authenticated internal
// inter-bank routes and by the inter-bank-aware /api/v3/transfers handler.
func NewInterBankServiceClient(addr string) (transactionpb.InterBankServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewInterBankServiceClient(conn), conn, nil
}
