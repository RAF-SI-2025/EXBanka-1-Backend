package grpcmw_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/shared/grpcmw"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func TestLoggingInterceptor_LogsErrorWithCode(t *testing.T) {
	buf := captureLog(t)
	icpt := grpcmw.UnaryLoggingInterceptor("test-service")

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}

	_, err := icpt(context.Background(), nil, info, handler)
	if err == nil {
		t.Fatal("expected error to propagate")
	}

	out := buf.String()
	for _, want := range []string{"test-service", "/Test/Method", "PermissionDenied"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q\ngot: %s", want, out)
		}
	}
}

func TestLoggingInterceptor_DoesNotLogOK(t *testing.T) {
	buf := captureLog(t)
	icpt := grpcmw.UnaryLoggingInterceptor("test-service")

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}

	_, _ = icpt(context.Background(), nil, info, handler)
	if buf.Len() != 0 {
		t.Errorf("expected no log on OK; got: %s", buf.String())
	}
}

func TestLoggingInterceptor_LogsWrappedError(t *testing.T) {
	buf := captureLog(t)
	icpt := grpcmw.UnaryLoggingInterceptor("test-service")

	base := errors.New("root cause")
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, base
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}

	_, _ = icpt(context.Background(), nil, info, handler)
	if !strings.Contains(buf.String(), "root cause") {
		t.Errorf("log should include original error: %s", buf.String())
	}
}
