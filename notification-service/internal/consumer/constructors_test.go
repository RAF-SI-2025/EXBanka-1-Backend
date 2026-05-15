package consumer

import (
	"context"
	"testing"
	"time"
)

func TestNewEmailConsumer_AndClose(t *testing.T) {
	c := NewEmailConsumer("127.0.0.1:1", nil, nil, nil)
	if c == nil || c.reader == nil {
		t.Fatal("nil consumer or reader")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestNewGeneralNotificationConsumer_AndClose(t *testing.T) {
	c := NewGeneralNotificationConsumer("127.0.0.1:1", nil, nil)
	if c == nil || c.reader == nil {
		t.Fatal("nil consumer or reader")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestNewVerificationConsumer_AndClose(t *testing.T) {
	c := NewVerificationConsumer("127.0.0.1:1", nil, nil, nil, nil)
	if c == nil || c.reader == nil {
		t.Fatal("nil consumer or reader")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestEmailConsumer_Start_CtxCancel(t *testing.T) {
	c := NewEmailConsumer("127.0.0.1:1", nil, nil, nil)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Start(ctx)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}

func TestGeneralNotificationConsumer_Start_CtxCancel(t *testing.T) {
	c := NewGeneralNotificationConsumer("127.0.0.1:1", nil, nil)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx) // this kicks off a goroutine; cancel exits it.
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(40 * time.Millisecond)
}

func TestVerificationConsumer_Start_CtxCancel(t *testing.T) {
	c := NewVerificationConsumer("127.0.0.1:1", nil, nil, nil, nil)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(40 * time.Millisecond)
}
