package grpc

import (
	adminpb "github.com/exbanka/contract/adminpb"
	"google.golang.org/grpc"
)

// AdminCronClient pairs a service name with its AdminCron gRPC client so the
// handler pool can fan-out by service label without a separate lookup map.
type AdminCronClient struct {
	Service string
	Client  adminpb.AdminCronClient
	Conn    *grpc.ClientConn
}

// NewAdminCronClient dials addr and wraps it in an AdminCronClient labelled
// with service. The caller owns the conn and must call Conn.Close() on
// shutdown.
func NewAdminCronClient(service, addr string) (*AdminCronClient, error) {
	conn, err := sagaDial(addr)
	if err != nil {
		return nil, err
	}
	return &AdminCronClient{
		Service: service,
		Client:  adminpb.NewAdminCronClient(conn),
		Conn:    conn,
	}, nil
}
