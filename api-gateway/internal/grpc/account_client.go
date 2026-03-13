package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	accountpb "github.com/exbanka/contract/accountpb"
)

func NewAccountClient(addr string) (accountpb.AccountServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return accountpb.NewAccountServiceClient(conn), conn, nil
}
