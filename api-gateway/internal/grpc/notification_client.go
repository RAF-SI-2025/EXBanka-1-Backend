package grpc

import (
	"google.golang.org/grpc"

	notificationpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/contract/shared"
)

func NewNotificationClient(addr string) (notificationpb.NotificationServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return notificationpb.NewNotificationServiceClient(conn), conn, nil
}
