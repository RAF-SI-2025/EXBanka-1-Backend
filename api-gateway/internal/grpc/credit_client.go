package grpc

import (
	"google.golang.org/grpc"

	creditpb "github.com/exbanka/contract/creditpb"
	"github.com/exbanka/contract/shared"
)

func NewCreditClient(addr string) (creditpb.CreditServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return creditpb.NewCreditServiceClient(conn), conn, nil
}
