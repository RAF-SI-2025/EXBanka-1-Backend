package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authpb "github.com/exbanka/contract/authpb"
)

func NewAuthClient(addr string) (authpb.AuthServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return authpb.NewAuthServiceClient(conn), conn, nil
}
