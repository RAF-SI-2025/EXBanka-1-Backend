package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	verificationpb "github.com/exbanka/contract/verificationpb"
)

func NewVerificationClient(addr string) (verificationpb.VerificationGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return verificationpb.NewVerificationGRPCServiceClient(conn), conn, nil
}
