package grpcmw

import (
	"context"

	"github.com/exbanka/contract/shared/saga"
)

// faultHeaderNames are the lower-cased gRPC-metadata keys carrying the
// X-Saga-* fault-injection directives. gRPC lowercases metadata keys, so we
// store/read them lowercased; saga.ParseFaultSpec matches case-insensitively.
var faultHeaderNames = []string{
	"x-saga-force-fail",
	"x-saga-force-fail-kind",
	"x-saga-compensate-fail",
	"x-saga-compensate-fail-times",
	"x-saga-inject-delay",
}

type faultHeadersKey struct{}

// WithFaultHeaders stashes raw X-Saga-* request headers (lowercased keys) on the
// context so the client interceptor can forward them as gRPC metadata. It is a
// no-op unless the binary was built with saga fault injection enabled
// (saga.FaultsEnabled) — in production the call returns ctx unchanged and the
// stash is never read.
func WithFaultHeaders(ctx context.Context, h map[string]string) context.Context {
	if !saga.FaultsEnabled || len(h) == 0 {
		return ctx
	}
	return context.WithValue(ctx, faultHeadersKey{}, h)
}

func faultHeadersFrom(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(faultHeadersKey{}).(map[string]string); ok {
		return v
	}
	return nil
}
