package grpc

import (
	"google.golang.org/grpc"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

func NewTransactionClient(addr string) (transactionpb.TransactionServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewTransactionServiceClient(conn), conn, nil
}

func NewFeeServiceClient(addr string) (transactionpb.FeeServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewFeeServiceClient(conn), conn, nil
}
