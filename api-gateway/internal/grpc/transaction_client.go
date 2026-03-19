package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

func NewTransactionClient(addr string) (transactionpb.TransactionServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewTransactionServiceClient(conn), conn, nil
}

func NewFeeServiceClient(addr string) (transactionpb.FeeServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return transactionpb.NewFeeServiceClient(conn), conn, nil
}
