package grpc

import (
	"google.golang.org/grpc"

	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/shared"
)

func NewExchangeClient(addr string) (exchangepb.ExchangeServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return exchangepb.NewExchangeServiceClient(conn), conn, nil
}
