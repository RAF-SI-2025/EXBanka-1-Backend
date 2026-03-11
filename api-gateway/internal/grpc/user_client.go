package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	userpb "github.com/exbanka/contract/userpb"
)

func NewUserClient(addr string) (userpb.UserServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewUserServiceClient(conn), conn, nil
}
