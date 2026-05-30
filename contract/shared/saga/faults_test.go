//go:build sagafaults

package saga

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/contract/shared"
)

// trackingSteps builds three steps that append their name to fwd/bwd slices.
func trackingSteps(fwd, bwd *[]string) []Step {
	mk := func(name string) Step {
		return Step{
			Name:     StepKind(name),
			Forward:  func(context.Context, *State) error { *fwd = append(*fwd, name); return nil },
			Backward: func(context.Context, *State) error { *bwd = append(*bwd, name); return nil },
		}
	}
	return []Step{mk("s1"), mk("s2"), mk("s3")}
}

func TestFault_ForceFailBefore_SkipsForwardAndCompensatesPrior(t *testing.T) {
	var fwd, bwd []string
	steps := trackingSteps(&fwd, &bwd)
	sg := NewSaga(nil).Add(steps[0]).Add(steps[1]).Add(steps[2])

	ctx := WithFaultSpec(context.Background(), FaultSpec{ForceFailStep: "s2", ForceFailKind: "before"})
	if err := sg.Execute(ctx, nil); err == nil {
		t.Fatal("expected forced failure error")
	}
	// s2 forward must NOT have run (failed before side effects); s3 unreached.
	if got := join(fwd); got != "s1" {
		t.Errorf("forward = %q, want \"s1\"", got)
	}
	// only s1 was completed, so only s1 compensates.
	if got := join(bwd); got != "s1" {
		t.Errorf("backward = %q, want \"s1\"", got)
	}
}

func TestFault_ForceFailAfter_RunsForwardThenCompensatesIncludingStep(t *testing.T) {
	var fwd, bwd []string
	steps := trackingSteps(&fwd, &bwd)
	sg := NewSaga(nil).Add(steps[0]).Add(steps[1]).Add(steps[2])

	ctx := WithFaultSpec(context.Background(), FaultSpec{ForceFailStep: "s2", ForceFailKind: "after"})
	if err := sg.Execute(ctx, nil); err == nil {
		t.Fatal("expected forced failure error")
	}
	// s2 forward ran (effect applied); s3 unreached.
	if got := join(fwd); got != "s1,s2" {
		t.Errorf("forward = %q, want \"s1,s2\"", got)
	}
	// s2 and s1 compensate in reverse order.
	if got := join(bwd); got != "s2,s1" {
		t.Errorf("backward = %q, want \"s2,s1\"", got)
	}
}

func TestFault_CompensateFailTimes_FailsThenSucceedsUnderRetry(t *testing.T) {
	var fwd, bwd []string
	steps := trackingSteps(&fwd, &bwd)
	sg := NewSaga(nil).
		WithRetry(shared.RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond}).
		Add(steps[0]).Add(steps[1]).Add(steps[2])

	// Force s3 to fail (rollback s2, s1); make s1's compensator fail once first.
	ctx := WithFaultSpec(context.Background(), FaultSpec{
		ForceFailStep:       "s3",
		CompensateFailStep:  "s1",
		CompensateFailTimes: 1,
	})
	if err := sg.Execute(ctx, nil); err == nil {
		t.Fatal("expected forced failure error")
	}
	// Despite the first compensator attempt failing, the retry must land s1's
	// backward, so both s2 and s1 end up compensated.
	if got := join(bwd); got != "s2,s1" {
		t.Errorf("backward = %q, want \"s2,s1\" (s1 should succeed on retry)", got)
	}
}

func TestFault_InjectDelay_SleepsBeforeStep(t *testing.T) {
	var fwd, bwd []string
	steps := trackingSteps(&fwd, &bwd)
	sg := NewSaga(nil).Add(steps[0]).Add(steps[1]).Add(steps[2])

	ctx := WithFaultSpec(context.Background(), FaultSpec{InjectDelayStep: "s2", InjectDelay: 120 * time.Millisecond})
	start := time.Now()
	if err := sg.Execute(ctx, nil); err != nil {
		t.Fatalf("happy path with delay should succeed: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("expected >=100ms elapsed from injected delay, got %v", elapsed)
	}
	if got := join(fwd); got != "s1,s2,s3" {
		t.Errorf("forward = %q, want all three", got)
	}
}

func TestParseFaultSpec_FromHeaders(t *testing.T) {
	f := ParseFaultSpec(map[string]string{
		"X-Saga-Force-Fail":            "settle_strike_buyer",
		"X-Saga-Force-Fail-Kind":       "after",
		"X-Saga-Compensate-Fail":       "reserve_strike",
		"X-Saga-Compensate-Fail-Times": "2",
		"X-Saga-Inject-Delay":          "credit_strike_seller:250",
	})
	if f.ForceFailStep != "settle_strike_buyer" || f.ForceFailKind != "after" {
		t.Errorf("force-fail parse: %+v", f)
	}
	if f.CompensateFailStep != "reserve_strike" || f.CompensateFailTimes != 2 {
		t.Errorf("compensate parse: %+v", f)
	}
	if f.InjectDelayStep != "credit_strike_seller" || f.InjectDelay != 250*time.Millisecond {
		t.Errorf("delay parse: %+v", f)
	}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
