package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	userpb "github.com/exbanka/contract/userpb"
)

// NewBlueprintClient creates a gRPC client for BlueprintService (user-service).
func NewBlueprintClient(addr string) (userpb.BlueprintServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return userpb.NewBlueprintServiceClient(conn), conn, nil
}
