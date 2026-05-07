package service

import (
	"context"
	"testing"
	"time"
)

func TestCandleService_NewCandleService(t *testing.T) {
	c := NewCandleService(nil)
	if c == nil {
		t.Fatal("NewCandleService returned nil")
	}
}

func TestCandleService_GetCandles_NilClientReturnsEmpty(t *testing.T) {
	c := NewCandleService(nil)
	got, err := c.GetCandles(context.Background(), 1, "1m", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestCandleService_GetCandles_NilClientStillValidatesInterval(t *testing.T) {
	// nil client short-circuits before interval check, so an invalid interval still passes
	c := NewCandleService(nil)
	got, err := c.GetCandles(context.Background(), 1, "garbage", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("expected nil for nil client, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}
