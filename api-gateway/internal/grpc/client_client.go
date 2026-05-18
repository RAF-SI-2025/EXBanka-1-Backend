package grpc

import (
	"google.golang.org/grpc"

	clientpb "github.com/exbanka/contract/clientpb"
)

func NewClientClient(addr string) (clientpb.ClientServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return clientpb.NewClientServiceClient(conn), conn, nil
}
