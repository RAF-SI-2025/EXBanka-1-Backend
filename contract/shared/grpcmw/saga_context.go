package grpcmw

import (
	"context"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/exbanka/contract/shared/saga"
)

const (
	mdSagaID   = "x-saga-id"
	mdSagaStep = "x-saga-step"
	mdActingID = "x-acting-employee-id"
)

// UnaryClientSagaContextInterceptor copies saga context from the call ctx onto
// outgoing gRPC metadata. Callees can read it back via UnarySagaContextInterceptor.
func UnaryClientSagaContextInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{},
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md := metadata.MD{}
		if id, ok := saga.SagaIDFromContext(ctx); ok {
			md.Set(mdSagaID, id)
		}
		if step, ok := saga.SagaStepFromContext(ctx); ok {
			md.Set(mdSagaStep, string(step))
		}
		if acting, ok := saga.ActingEmployeeIDFromContext(ctx); ok {
			md.Set(mdActingID, strconv.FormatUint(acting, 10))
		}
		ctx = metadata.NewOutgoingContext(ctx, md)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnarySagaContextInterceptor extracts saga metadata from incoming RPCs into
// the context. Repositories read these values and stamp them onto side-effect rows.
func UnarySagaContextInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (interface{}, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get(mdSagaID); len(v) > 0 {
				ctx = saga.WithSagaID(ctx, v[0])
			}
			if v := md.Get(mdSagaStep); len(v) > 0 {
				ctx = saga.WithSagaStep(ctx, saga.StepKind(v[0]))
			}
			if v := md.Get(mdActingID); len(v) > 0 {
				if n, err := strconv.ParseUint(v[0], 10, 64); err == nil {
					ctx = saga.WithActingEmployeeID(ctx, n)
				}
			}
		}
		return handler(ctx, req)
	}
}
