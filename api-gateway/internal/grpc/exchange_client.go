package grpc

import (
	exchangepb "github.com/exbanka/contract/exchangepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func NewExchangeClient(addr string) (exchangepb.ExchangeServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return exchangepb.NewExchangeServiceClient(conn), conn, nil
}
