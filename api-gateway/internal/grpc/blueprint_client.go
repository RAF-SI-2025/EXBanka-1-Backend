package grpc

import (
	"google.golang.org/grpc"

	userpb "github.com/exbanka/contract/userpb"
)

// NewBlueprintClient creates a gRPC client for BlueprintService (user-service).
func NewBlueprintClient(addr string) (userpb.BlueprintServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewBlueprintServiceClient(conn), conn, nil
}
