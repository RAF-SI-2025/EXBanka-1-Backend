package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	userpb "github.com/exbanka/contract/userpb"
	clientpb "github.com/exbanka/contract/clientpb"
)

// NewEmployeeLimitClient creates a gRPC client for EmployeeLimitService (user-service).
func NewEmployeeLimitClient(addr string) (userpb.EmployeeLimitServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewEmployeeLimitServiceClient(conn), conn, nil
}

// NewClientLimitClient creates a gRPC client for ClientLimitService (client-service).
func NewClientLimitClient(addr string) (clientpb.ClientLimitServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return clientpb.NewClientLimitServiceClient(conn), conn, nil
}
