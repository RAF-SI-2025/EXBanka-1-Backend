package grpcmw_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/exbanka/contract/shared/grpcmw"
	"github.com/exbanka/contract/shared/saga"
)

func TestOutgoingInterceptor_AttachesSagaMetadata(t *testing.T) {
	icpt := grpcmw.UnaryClientSagaContextInterceptor()

	captured := metadata.MD{}
	invoker := func(ctx context.Context, method string, req, reply interface{},
		cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		captured = md
		return nil
	}

	ctx := saga.WithSagaID(context.Background(), "saga-1")
	ctx = saga.WithSagaStep(ctx, saga.StepDebitBuyer)
	ctx = saga.WithActingEmployeeID(ctx, 7)

	_ = icpt(ctx, "/Test/Method", nil, nil, nil, invoker)

	if got := captured.Get("x-saga-id"); len(got) == 0 || got[0] != "saga-1" {
		t.Errorf("x-saga-id missing/wrong: %v", got)
	}
	if got := captured.Get("x-saga-step"); len(got) == 0 || got[0] != "debit_buyer" {
		t.Errorf("x-saga-step missing/wrong: %v", got)
	}
	if got := captured.Get("x-acting-employee-id"); len(got) == 0 || got[0] != "7" {
		t.Errorf("x-acting-employee-id missing/wrong: %v", got)
	}
}

func TestIncomingInterceptor_RestoresSagaContext(t *testing.T) {
	icpt := grpcmw.UnarySagaContextInterceptor()

	md := metadata.New(map[string]string{
		"x-saga-id":            "saga-2",
		"x-saga-step":          "credit_seller",
		"x-acting-employee-id": "12",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var seenSagaID string
	var seenStep saga.StepKind
	var seenActor uint64
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		seenSagaID, _ = saga.SagaIDFromContext(ctx)
		seenStep, _ = saga.SagaStepFromContext(ctx)
		seenActor, _ = saga.ActingEmployeeIDFromContext(ctx)
		return nil, nil
	}

	_, _ = icpt(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}, handler)

	if seenSagaID != "saga-2" {
		t.Errorf("saga id: %q", seenSagaID)
	}
	if seenStep != saga.StepCreditSeller {
		t.Errorf("step: %q", seenStep)
	}
	if seenActor != 12 {
		t.Errorf("actor: %d", seenActor)
	}
}
