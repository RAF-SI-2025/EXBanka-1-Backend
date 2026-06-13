//go:build sagafaults

package saga

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FaultsEnabled is true only in test builds compiled with `-tags sagafaults`.
// The build tag IS the production guard: untagged binaries link
// faults_disabled.go instead, so the fault-injection code is physically absent
// from production and cannot be triggered by any input. Services MAY
// additionally gate on this constant at startup (e.g. log.Fatal unless an
// explicit test-env var is set) for defence in depth when deliberately
// building a fault-enabled image; that runtime guard is wired per-service.
const FaultsEnabled = true

// faultSpecFrom extracts a FaultSpec from ctx, or the zero value if absent.
// Lives in the enabled file because only the active hooks consult it.
func faultSpecFrom(ctx context.Context) FaultSpec {
	if v, ok := ctx.Value(faultCtxKey{}).(FaultSpec); ok {
		return v
	}
	return FaultSpec{}
}

// compFailCounts tracks, per (sagaID|step), how many times a forced
// compensation failure has already fired, so "fail N times then succeed" works
// across in-saga retries and same-process recovery re-invocations.
var compFailCounts sync.Map // string -> int

func faultDelay(ctx context.Context, step StepKind) {
	f := faultSpecFrom(ctx)
	if f.InjectDelayStep == step && f.InjectDelay > 0 {
		select {
		case <-time.After(f.InjectDelay):
		case <-ctx.Done():
		}
	}
}

func faultBeforeForward(ctx context.Context, step StepKind) error {
	f := faultSpecFrom(ctx)
	if f.ForceFailStep == step && f.ForceFailKind != "after" {
		return fmt.Errorf("saga fault: forced failure of step %s (before side effects)", step)
	}
	return nil
}

func faultAfterForward(ctx context.Context, step StepKind) error {
	f := faultSpecFrom(ctx)
	if f.ForceFailStep == step && f.ForceFailKind == "after" {
		return fmt.Errorf("saga fault: forced failure of step %s (after side effects)", step)
	}
	return nil
}

func faultCompensate(ctx context.Context, sagaID string, step StepKind) error {
	f := faultSpecFrom(ctx)
	if f.CompensateFailStep != step || f.CompensateFailTimes <= 0 {
		return nil
	}
	key := sagaID + "|" + string(step)
	v, _ := compFailCounts.LoadOrStore(key, 0)
	attempts := v.(int)
	if attempts < f.CompensateFailTimes {
		compFailCounts.Store(key, attempts+1)
		return fmt.Errorf("saga fault: forced compensation failure %d/%d of step %s",
			attempts+1, f.CompensateFailTimes, step)
	}
	return nil
}
