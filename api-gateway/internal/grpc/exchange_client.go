package grpc

import (
	"google.golang.org/grpc"

	exchangepb "github.com/exbanka/contract/exchangepb"
)

func NewExchangeClient(addr string) (exchangepb.ExchangeServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return exchangepb.NewExchangeServiceClient(conn), conn, nil
}
