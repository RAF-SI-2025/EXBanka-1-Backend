package grpc

import (
	"google.golang.org/grpc"

	"github.com/exbanka/contract/shared"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

func NewVerificationClient(addr string) (verificationpb.VerificationGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return verificationpb.NewVerificationGRPCServiceClient(conn), conn, nil
}
