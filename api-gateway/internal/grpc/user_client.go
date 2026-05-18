package grpc

import (
	"google.golang.org/grpc"

	userpb "github.com/exbanka/contract/userpb"
)

func NewUserClient(addr string) (userpb.UserServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewUserServiceClient(conn), conn, nil
}

func NewActuaryClient(addr string) (userpb.ActuaryServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewActuaryServiceClient(conn), conn, nil
}
