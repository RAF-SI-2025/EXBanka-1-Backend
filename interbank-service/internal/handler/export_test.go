package handler

import "time"

// Test-only accessors (compiled only under `go test`). The 202-async tests live
// in package handler_test (black-box), so they can't reach the unexported
// workerTimeout field / inflight map directly — these expose just enough to
// drive the worker-timeout behaviour deterministically.

// SetWorkerTimeout overrides the bounded background-worker context timeout.
func (h *PeerTxGRPCHandler) SetWorkerTimeout(d time.Duration) {
	h.workerTimeout = d
}

// InflightLen reports how many in-flight reserve workers are currently tracked.
// Used by tests to wait until a worker goroutine has exited.
func (h *PeerTxGRPCHandler) InflightLen() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.inflight)
}
