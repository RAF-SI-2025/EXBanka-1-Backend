package service

import (
	"context"
	"testing"
	"time"
)

// TestCandleService_GetCandles_NilInflux returns empty when not wired.
func TestCandleService_GetCandles_NilInflux(t *testing.T) {
	svc := NewCandleService(nil)
	got, err := svc.GetCandles(context.Background(), 1, "1m", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}
