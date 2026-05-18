package grpc

import (
	"google.golang.org/grpc"

	verificationpb "github.com/exbanka/contract/verificationpb"
)

func NewVerificationClient(addr string) (verificationpb.VerificationGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return verificationpb.NewVerificationGRPCServiceClient(conn), conn, nil
}
