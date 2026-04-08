package grpc

import (
	"google.golang.org/grpc"

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/shared"
	userpb "github.com/exbanka/contract/userpb"
)

// NewEmployeeLimitClient creates a gRPC client for EmployeeLimitService (user-service).
func NewEmployeeLimitClient(addr string) (userpb.EmployeeLimitServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewEmployeeLimitServiceClient(conn), conn, nil
}

// NewClientLimitClient creates a gRPC client for ClientLimitService (client-service).
func NewClientLimitClient(addr string) (clientpb.ClientLimitServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return clientpb.NewClientLimitServiceClient(conn), conn, nil
}
