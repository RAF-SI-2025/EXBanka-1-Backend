package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newTestCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	c, err := NewRedisCache(mr.Addr())
	if err != nil {
		mr.Close()
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		mr.Close()
	})
	return c, mr
}

func TestNewRedisCache_DialFailure(t *testing.T) {
	if _, err := NewRedisCache("127.0.0.1:1"); err == nil {
		t.Fatal("expected dial error against unused port")
	}
}

func TestRedisCache_SetGetDelete(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()
	type payload struct {
		N int `json:"n"`
	}
	if err := c.Set(ctx, "k", payload{N: 7}, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	var got payload
	if err := c.Get(ctx, "k", &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.N != 7 {
		t.Fatalf("got %+v", got)
	}
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.Get(ctx, "k", &got); err == nil {
		t.Fatal("expected miss after delete")
	}
}

func TestRedisCache_SetMarshalError(t *testing.T) {
	c, _ := newTestCache(t)
	// channels can't be JSON-marshaled
	if err := c.Set(context.Background(), "k", make(chan int), time.Second); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestRedisCache_DeleteByPattern(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()
	for _, k := range []string{"foo:1", "foo:2", "bar:1"} {
		if err := c.Set(ctx, k, "v", time.Minute); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	if err := c.DeleteByPattern(ctx, "foo:*"); err != nil {
		t.Fatalf("delete by pattern: %v", err)
	}
	var v string
	if err := c.Get(ctx, "foo:1", &v); err == nil {
		t.Fatal("foo:1 should be gone")
	}
	if err := c.Get(ctx, "bar:1", &v); err != nil {
		t.Fatalf("bar:1 should remain: %v", err)
	}
}
