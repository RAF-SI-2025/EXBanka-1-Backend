package grpc

import (
	"google.golang.org/grpc"

	notificationpb "github.com/exbanka/contract/notificationpb"
)

func NewNotificationClient(addr string) (notificationpb.NotificationServiceClient, *grpc.ClientConn, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, nil, err
	}
	return notificationpb.NewNotificationServiceClient(conn), conn, nil
}
