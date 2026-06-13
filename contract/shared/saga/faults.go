package saga

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// FaultSpec describes deterministic faults a test wants the executor to inject
// into a saga run. It is carried on the context (set at the gRPC boundary from
// X-Saga-* request metadata) and consulted by the executor's fault hooks.
//
// IMPORTANT: the hooks that ACT on a FaultSpec are compiled only under the
// `sagafaults` build tag (see faults_enabled.go / faults_disabled.go). A
// production binary built without that tag never injects a fault even if a
// FaultSpec somehow reaches it — the act functions are no-ops. This type and
// its parser are always compiled because they are inert data; only the
// behaviour is gated.
//
// Header → field mapping (format is implementation-defined per SAGA.pdf; we key
// faults by the saga's own StepKind names rather than F1..F5 so they are
// self-describing):
//
//	X-Saga-Force-Fail:          <step-name>     → ForceFailStep
//	X-Saga-Force-Fail-Kind:     before | after  → ForceFailKind (default "before")
//	X-Saga-Compensate-Fail:     <step-name>     → CompensateFailStep
//	X-Saga-Compensate-Fail-Times: <n>           → CompensateFailTimes
//	X-Saga-Inject-Delay:        <step-name>:<ms> → InjectDelayStep / InjectDelay
type FaultSpec struct {
	ForceFailStep       StepKind
	ForceFailKind       string // "before" (default) or "after" side effects
	CompensateFailStep  StepKind
	CompensateFailTimes int
	InjectDelayStep     StepKind
	InjectDelay         time.Duration
}

// HasAny reports whether the spec requests any fault at all.
func (f FaultSpec) HasAny() bool {
	return f.ForceFailStep != "" || f.CompensateFailStep != "" || f.InjectDelayStep != ""
}

type faultCtxKey struct{}

// WithFaultSpec returns a context carrying the given fault spec. Called at the
// gRPC boundary in test builds; harmless in production (the act hooks ignore it).
func WithFaultSpec(ctx context.Context, spec FaultSpec) context.Context {
	return context.WithValue(ctx, faultCtxKey{}, spec)
}

// ParseFaultSpec builds a FaultSpec from a flat header/metadata map. Keys are
// matched case-insensitively against the canonical X-Saga-* names. Unknown or
// malformed values are ignored (best-effort: a test harness controls these).
func ParseFaultSpec(headers map[string]string) FaultSpec {
	get := func(name string) string {
		lname := strings.ToLower(name)
		for k, v := range headers {
			if strings.ToLower(k) == lname {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	var f FaultSpec
	f.ForceFailStep = StepKind(get("X-Saga-Force-Fail"))
	f.ForceFailKind = strings.ToLower(get("X-Saga-Force-Fail-Kind"))
	if f.ForceFailKind != "after" {
		f.ForceFailKind = "before"
	}
	f.CompensateFailStep = StepKind(get("X-Saga-Compensate-Fail"))
	if n, err := strconv.Atoi(get("X-Saga-Compensate-Fail-Times")); err == nil && n > 0 {
		f.CompensateFailTimes = n
	}
	if d := get("X-Saga-Inject-Delay"); d != "" {
		// format: <step-name>:<milliseconds>
		if i := strings.LastIndex(d, ":"); i > 0 {
			if ms, err := strconv.Atoi(d[i+1:]); err == nil && ms > 0 {
				f.InjectDelayStep = StepKind(d[:i])
				f.InjectDelay = time.Duration(ms) * time.Millisecond
			}
		}
	}
	return f
}
