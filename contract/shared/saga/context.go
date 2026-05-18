package saga

import "context"

// ctxKey is an unexported type used to namespace saga-related context keys.
// Using a distinct unexported type avoids collisions with other packages that
// stash values onto the same context.
type ctxKey int

const (
	keySagaID ctxKey = iota
	keySagaStep
	keyActingEmployeeID
)

// WithSagaID returns a context carrying the given saga ID. Side-effect
// repositories read this value to stamp it onto rows for end-to-end audit.
func WithSagaID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keySagaID, id)
}

// WithSagaStep returns a context carrying the current saga step kind.
func WithSagaStep(ctx context.Context, step StepKind) context.Context {
	return context.WithValue(ctx, keySagaStep, step)
}

// WithActingEmployeeID returns a context carrying the employee ID that
// initiated the saga (for audit trails on side-effect rows).
func WithActingEmployeeID(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, keyActingEmployeeID, id)
}

// SagaIDFromContext returns the saga ID stored on ctx, if any.
func SagaIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(keySagaID).(string)
	return v, ok
}

// SagaStepFromContext returns the saga step kind stored on ctx, if any.
func SagaStepFromContext(ctx context.Context) (StepKind, bool) {
	v, ok := ctx.Value(keySagaStep).(StepKind)
	return v, ok
}

// ActingEmployeeIDFromContext returns the acting employee ID stored on ctx, if any.
func ActingEmployeeIDFromContext(ctx context.Context) (uint64, bool) {
	v, ok := ctx.Value(keyActingEmployeeID).(uint64)
	return v, ok
}
