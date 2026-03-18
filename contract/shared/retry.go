package shared

import (
	"context"
	"time"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

// DefaultRetryConfig for cross-service gRPC calls.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   500 * time.Millisecond,
}

// Retry executes fn up to MaxAttempts times with exponential backoff.
// Returns the last error if all attempts fail.
func Retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for i := 0; i < cfg.MaxAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if i < cfg.MaxAttempts-1 {
			delay := cfg.BaseDelay * time.Duration(1<<uint(i))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}
