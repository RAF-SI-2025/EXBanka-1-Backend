package grpc

import (
	"google.golang.org/grpc"

	cardpb "github.com/exbanka/contract/cardpb"
	"github.com/exbanka/contract/shared"
)

func NewCardClient(addr string) (cardpb.CardServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return cardpb.NewCardServiceClient(conn), conn, nil
}

func NewVirtualCardClient(addr string) (cardpb.VirtualCardServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return cardpb.NewVirtualCardServiceClient(conn), conn, nil
}

func NewCardRequestClient(addr string) (cardpb.CardRequestServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return cardpb.NewCardRequestServiceClient(conn), conn, nil
}
