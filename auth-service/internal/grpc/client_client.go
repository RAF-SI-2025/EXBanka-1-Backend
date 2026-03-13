package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	clientpb "github.com/exbanka/contract/clientpb"
)

// NewClientServiceClient creates a gRPC client connected to the client-service.
func NewClientServiceClient(addr string) (clientpb.ClientServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return clientpb.NewClientServiceClient(conn), conn, nil
}
