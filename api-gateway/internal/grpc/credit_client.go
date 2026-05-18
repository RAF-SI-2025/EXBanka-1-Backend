package grpc

import (
	"google.golang.org/grpc"

	creditpb "github.com/exbanka/contract/creditpb"
)

func NewCreditClient(addr string) (creditpb.CreditServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return creditpb.NewCreditServiceClient(conn), conn, nil
}
