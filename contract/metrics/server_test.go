package metrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// getFreePort asks the OS for an available TCP port.
func getFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func TestStartMetricsServer_MetricsEndpoint(t *testing.T) {
	port := getFreePort(t)
	_, _, shutdown := StartMetricsServer(port)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Errorf("shutdown error: %v", err)
		}
	}()

	// Give the server a moment to start.
	time.Sleep(50 * time.Millisecond)

	url := fmt.Sprintf("http://127.0.0.1:%s/metrics", port)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /metrics: expected 200, got %d", resp.StatusCode)
	}
}

func TestStartMetricsServer_HealthEndpoint(t *testing.T) {
	port := getFreePort(t)
	_, _, shutdown := StartMetricsServer(port)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Errorf("shutdown error: %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	url := fmt.Sprintf("http://127.0.0.1:%s/health", port)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health: expected 200, got %d", resp.StatusCode)
	}
}

func TestStartMetricsServer_Shutdown(t *testing.T) {
	port := getFreePort(t)
	_, _, shutdown := StartMetricsServer(port)

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}

	// After shutdown, the server should no longer be accepting connections.
	url := fmt.Sprintf("http://127.0.0.1:%s/metrics", port)
	_, err := http.Get(url)
	if err == nil {
		t.Error("expected connection error after shutdown, got nil")
	}
}

func TestStartMetricsServer_LivezEndpoint(t *testing.T) {
	port := getFreePort(t)
	_, _, shutdown := StartMetricsServer(port)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/livez", port))
	if err != nil {
		t.Fatalf("GET /livez failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /livez: expected 200, got %d", resp.StatusCode)
	}
}

func TestStartMetricsServer_StartupzEndpoint(t *testing.T) {
	port := getFreePort(t)
	markReady, _, shutdown := StartMetricsServer(port)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	// Before markReady, should be 503
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/startupz", port))
	if err != nil {
		t.Fatalf("GET /startupz failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /startupz before ready: expected 503, got %d", resp.StatusCode)
	}

	markReady()

	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/startupz", port))
	if err != nil {
		t.Fatalf("GET /startupz failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /startupz after ready: expected 200, got %d", resp.StatusCode)
	}
}

func TestStartMetricsServer_ReadyzEndpoint(t *testing.T) {
	port := getFreePort(t)
	markReady, addCheck, shutdown := StartMetricsServer(port)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	// Before markReady, 503
	resp, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%s/readyz", port))
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz before ready: expected 503, got %d", resp.StatusCode)
	}

	markReady()

	// After markReady, no checks registered, 200
	resp, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%s/readyz", port))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /readyz after ready, no checks: expected 200, got %d", resp.StatusCode)
	}

	// Register a failing check
	addCheck(func(ctx context.Context) error {
		return fmt.Errorf("db connection lost")
	})

	resp, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%s/readyz", port))
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz with failing check: expected 503, got %d", resp.StatusCode)
	}
}
