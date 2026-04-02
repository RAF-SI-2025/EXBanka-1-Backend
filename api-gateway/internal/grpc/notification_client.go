package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notificationpb "github.com/exbanka/contract/notificationpb"
)

func NewNotificationClient(addr string) (notificationpb.NotificationServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return notificationpb.NewNotificationServiceClient(conn), conn, nil
}
