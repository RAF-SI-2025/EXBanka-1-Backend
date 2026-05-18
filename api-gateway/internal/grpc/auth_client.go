package grpc

import (
	"google.golang.org/grpc"

	authpb "github.com/exbanka/contract/authpb"
)

func NewAuthClient(addr string) (authpb.AuthServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return authpb.NewAuthServiceClient(conn), conn, nil
}
