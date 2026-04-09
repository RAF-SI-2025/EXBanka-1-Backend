package metrics

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ReadinessCheck is a function that returns nil if a dependency is healthy.
type ReadinessCheck func(ctx context.Context) error

// StartMetricsServer starts an HTTP server exposing:
//
//	/metrics  — Prometheus scrape endpoint
//	/health   — legacy liveness probe (always 200, kept for Prometheus/backwards compat)
//	/livez    — liveness probe (always 200)
//	/readyz   — readiness probe (503 until started AND all checks pass)
//	/startupz — startup probe (503 until started, then 200)
func StartMetricsServer(port string) (func(), func(ReadinessCheck), func(ctx context.Context) error) {
	started := &atomic.Bool{}
	var checks []ReadinessCheck
	var checksMu sync.Mutex

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	livenessHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
	mux.HandleFunc("/health", livenessHandler)
	mux.HandleFunc("/livez", livenessHandler)

	mux.HandleFunc("/startupz", func(w http.ResponseWriter, _ *http.Request) {
		if started.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("STARTED"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("STARTING"))
		}
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !started.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("NOT STARTED"))
			return
		}

		checksMu.Lock()
		currentChecks := make([]ReadinessCheck, len(checks))
		copy(currentChecks, checks)
		checksMu.Unlock()

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		for _, check := range currentChecks {
			if err := check(ctx); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("NOT READY: " + err.Error()))
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		log.Printf("metrics server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	markReady := func() { started.Store(true) }

	addCheck := func(c ReadinessCheck) {
		checksMu.Lock()
		checks = append(checks, c)
		checksMu.Unlock()
	}

	shutdown := func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		fmt.Println("shutting down metrics server...")
		return srv.Shutdown(shutdownCtx)
	}

	return markReady, addCheck, shutdown
}
