package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	creditpb "github.com/exbanka/contract/creditpb"
)

func NewCreditClient(addr string) (creditpb.CreditServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return creditpb.NewCreditServiceClient(conn), conn, nil
}
