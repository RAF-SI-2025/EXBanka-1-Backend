package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessWithRetry_SuccessFirstTry(t *testing.T) {
	calls := 0
	err := processWithRetry(context.Background(), "t", func(context.Context, []byte) error {
		calls++
		return nil
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestProcessWithRetry_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	err := processWithRetry(context.Background(), "t", func(context.Context, []byte) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestProcessWithRetry_ExhaustsAndReturnsLastError(t *testing.T) {
	calls := 0
	want := errors.New("always")
	err := processWithRetry(context.Background(), "t", func(context.Context, []byte) error {
		calls++
		return want
	}, nil)
	require.ErrorIs(t, err, want)
	assert.Equal(t, maxProcessAttempts, calls)
}

func TestProcessWithRetry_CtxCancelStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	calls := 0
	err := processWithRetry(ctx, "t", func(context.Context, []byte) error {
		calls++
		return errors.New("fail")
	}, nil)
	require.Error(t, err)
	// One attempt runs; the backoff select then observes ctx.Done and bails,
	// so we do NOT burn all attempts on a cancelled context.
	assert.Equal(t, 1, calls)
}
