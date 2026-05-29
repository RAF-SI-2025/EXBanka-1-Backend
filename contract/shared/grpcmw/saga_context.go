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
		// Forward X-Saga-* fault directives (test builds only; dead code when
		// saga.FaultsEnabled is the production false constant).
		if saga.FaultsEnabled {
			for k, v := range faultHeadersFrom(ctx) {
				md.Set(k, v)
			}
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
			// Extract X-Saga-* fault directives into a FaultSpec on the context
			// (test builds only; dead code when saga.FaultsEnabled is false).
			if saga.FaultsEnabled {
				fh := make(map[string]string, len(faultHeaderNames))
				for _, k := range faultHeaderNames {
					if v := md.Get(k); len(v) > 0 {
						fh[k] = v[0]
					}
				}
				if len(fh) > 0 {
					if spec := saga.ParseFaultSpec(fh); spec.HasAny() {
						ctx = saga.WithFaultSpec(ctx, spec)
					}
				}
			}
		}
		return handler(ctx, req)
	}
}
