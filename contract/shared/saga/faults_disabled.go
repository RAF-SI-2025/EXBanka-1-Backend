//go:build !sagafaults

package saga

import "context"

// FaultsEnabled reports whether saga fault-injection is compiled in. False in
// every build that lacks the `sagafaults` tag — i.e. all production builds —
// so the executor's fault hooks below are guaranteed no-ops and faults cannot
// be injected no matter what reaches the context.
const FaultsEnabled = false

func faultDelay(context.Context, StepKind)                    {}
func faultBeforeForward(context.Context, StepKind) error      { return nil }
func faultAfterForward(context.Context, StepKind) error       { return nil }
func faultCompensate(context.Context, string, StepKind) error { return nil }
