package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cardpb "github.com/exbanka/contract/cardpb"
)

func NewCardClient(addr string) (cardpb.CardServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return cardpb.NewCardServiceClient(conn), conn, nil
}
