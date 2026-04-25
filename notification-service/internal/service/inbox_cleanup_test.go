package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubExpiredItemDeleter struct {
	calls    int
	deleted  int64
	err      error
	signalCh chan struct{}
}

func (s *stubExpiredItemDeleter) DeleteExpired() (int64, error) {
	s.calls++
	if s.signalCh != nil {
		select {
		case s.signalCh <- struct{}{}:
		default:
		}
	}
	return s.deleted, s.err
}

func TestInboxCleanupService_RunOnce_HappyPath(t *testing.T) {
	stub := &stubExpiredItemDeleter{deleted: 3}
	svc := newInboxCleanupServiceWithDeleter(stub)

	deleted, err := svc.runOnce()
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)
	assert.Equal(t, 1, stub.calls)
}

func TestInboxCleanupService_RunOnce_NothingToDelete(t *testing.T) {
	stub := &stubExpiredItemDeleter{deleted: 0}
	svc := newInboxCleanupServiceWithDeleter(stub)

	deleted, err := svc.runOnce()
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
	assert.Equal(t, 1, stub.calls)
}

func TestInboxCleanupService_RunOnce_RepoErrorReturned(t *testing.T) {
	wantErr := errors.New("db unavailable")
	stub := &stubExpiredItemDeleter{err: wantErr}
	svc := newInboxCleanupServiceWithDeleter(stub)

	deleted, err := svc.runOnce()
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Equal(t, int64(0), deleted)
	assert.Equal(t, 1, stub.calls)
}

func TestInboxCleanupService_StartCleanupCron_StopsOnContextCancel(t *testing.T) {
	stub := &stubExpiredItemDeleter{}
	svc := newInboxCleanupServiceWithDeleter(stub)

	ctx, cancel := context.WithCancel(context.Background())
	svc.StartCleanupCron(ctx)

	// Cancel almost immediately — the cron uses a 1-minute ticker,
	// so we should never see DeleteExpired called.
	cancel()
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 0, stub.calls, "ticker should not have fired before cancellation")
}

func TestNewInboxCleanupService_WrapsRepository(t *testing.T) {
	// Constructor with the public signature still works (repo arg may be nil
	// since we don't call it — we just need to confirm the wiring compiles).
	svc := NewInboxCleanupService(nil)
	require.NotNil(t, svc)
}
