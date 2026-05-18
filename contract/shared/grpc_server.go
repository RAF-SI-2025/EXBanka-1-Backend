// Package shared — grpc_server.go is the canonical gRPC server bootstrap.
//
// Every service's cmd/main.go does the same dance: net.Listen on a port,
// construct grpc.NewServer with interceptors, register the service
// handler, spawn a goroutine that calls server.Serve, then wait for
// SIGTERM/SIGINT and call GracefulStop.
//
// This helper extracts the dance. Callers supply a register callback
// that receives the server and binds whatever services they expose. The
// helper handles listening, serving, and graceful shutdown.
package shared

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
)

// GRPCServerConfig controls bootstrap behavior.
type GRPCServerConfig struct {
	// Address to listen on, in net.Listen format (e.g., ":50057",
	// "0.0.0.0:50057").
	Address string

	// Register is the callback that binds service handlers to the
	// server. Called once before Serve. Required.
	Register func(*grpc.Server)

	// Options to pass to grpc.NewServer (interceptors, TLS creds,
	// keepalive). Optional.
	Options []grpc.ServerOption

	// Signals to listen for as shutdown triggers. Defaults to
	// SIGINT, SIGTERM. Override to add SIGHUP for log reopen, etc.
	Signals []os.Signal

	// ShutdownTimeout caps how long GracefulStop may block before the
	// helper falls back to Stop (which kills in-flight RPCs).
	// Defaults to 30s.
	ShutdownTimeout time.Duration

	// OnReady, when non-nil, is called once the listener is open and
	// the server is about to start accepting RPCs. Useful for
	// readiness probes or "service started" log lines.
	OnReady func()
}

// RunGRPCServer is the all-in-one helper. It listens on cfg.Address,
// registers service handlers, serves until ctx is cancelled or a shutdown
// signal arrives, then performs a bounded GracefulStop.
//
// Returns the first non-nil error encountered: listen failure, Serve
// failure, or context cancellation cause. A clean shutdown returns nil.
//
// Typical usage in cmd/main.go:
//
//	ctx, stop := signal.NotifyContext(context.Background(),
//	    syscall.SIGINT, syscall.SIGTERM)
//	defer stop()
//
//	err := shared.RunGRPCServer(ctx, shared.GRPCServerConfig{
//	    Address: ":50057",
//	    Register: func(s *grpc.Server) {
//	        transactionpb.RegisterTransactionServiceServer(s, handler)
//	    },
//	})
//	if err != nil { log.Fatal(err) }
func RunGRPCServer(ctx context.Context, cfg GRPCServerConfig) error {
	if cfg.Register == nil {
		return fmt.Errorf("grpc: nil Register callback")
	}
	if cfg.Address == "" {
		return fmt.Errorf("grpc: empty Address")
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}

	lis, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("grpc: listen %s: %w", cfg.Address, err)
	}

	srv := grpc.NewServer(cfg.Options...)
	cfg.Register(srv)

	// Wire signal handling into the supplied ctx. Callers that already
	// pass a signal-aware context (signal.NotifyContext) can leave Signals
	// nil; this just augments the trigger set.
	shutdownCtx := ctx
	if len(cfg.Signals) > 0 {
		var stop context.CancelFunc
		shutdownCtx, stop = signalContext(ctx, cfg.Signals...)
		defer stop()
	}

	// Serve in a goroutine so we can race shutdown signals against it.
	serveErr := make(chan error, 1)
	go func() {
		if cfg.OnReady != nil {
			cfg.OnReady()
		}
		defaultLog("grpc: serving on %s", cfg.Address)
		serveErr <- srv.Serve(lis)
	}()

	select {
	case err := <-serveErr:
		// Serve returned on its own (listener closed, fatal error).
		if err != nil {
			return fmt.Errorf("grpc: serve: %w", err)
		}
		return nil

	case <-shutdownCtx.Done():
		defaultLog("grpc: shutdown signal received, stopping gracefully (timeout=%s)", cfg.ShutdownTimeout)
		stopped := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
			defaultLog("grpc: graceful stop complete")
		case <-time.After(cfg.ShutdownTimeout):
			defaultLog("grpc: graceful stop timed out, forcing Stop")
			srv.Stop()
		}
		// Drain serveErr so the goroutine doesn't leak.
		<-serveErr
		return nil
	}
}

// signalContext returns a context cancelled when ctx itself is cancelled
// or when any of sigs is received. Caller must invoke the returned cancel
// to release signal handlers.
func signalContext(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	go func() {
		select {
		case <-ctx.Done():
		case <-ch:
			cancel()
		}
		signal.Stop(ch)
	}()
	return ctx, cancel
}

// DefaultShutdownSignals is the conventional set: SIGINT (Ctrl+C in dev)
// and SIGTERM (Docker / Kubernetes). Exposed so callers can reference the
// canonical list rather than hand-rolling it.
var DefaultShutdownSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
