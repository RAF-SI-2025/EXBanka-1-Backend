package grpc

import (
	"google.golang.org/grpc"

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/shared"
)

func NewClientClient(addr string) (clientpb.ClientServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return clientpb.NewClientServiceClient(conn), conn, nil
}
