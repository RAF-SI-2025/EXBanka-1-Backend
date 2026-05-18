package grpc

import (
	"google.golang.org/grpc"

	"github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
)

// sagaDial wraps shared.DialGRPC with the saga-context client interceptor so
// every outgoing RPC carries x-saga-id/x-saga-step metadata when the caller's
// context has them set. Use this in place of shared.DialGRPC for any service
// client created here.
//
// We can't put this in contract/shared itself: shared/saga imports shared,
// and contract/shared/grpcmw imports shared/saga, so wiring the interceptor
// inside shared.DialGRPC would form a cycle.
func sagaDial(addr string) (*grpc.ClientConn, error) {
	return shared.DialGRPC(addr, grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()))
}
