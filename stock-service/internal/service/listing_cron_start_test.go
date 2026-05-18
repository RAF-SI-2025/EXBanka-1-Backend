package service

import (
	"context"
	"testing"
	"time"
)

// TestListingCron_StartDailyCron_Cancels covers the StartDailyCron goroutine
// path and its ctx.Done() exit branch.
func TestListingCron_StartDailyCron_Cancels(t *testing.T) {
	svc := NewListingCronService(&listingCronListingMock{}, &listingCronDailyMock{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	svc.StartDailyCron(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
}
