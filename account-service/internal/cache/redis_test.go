package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisCache_RoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := NewRedisCache(mr.Addr())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Set(ctx, "k", map[string]string{"x": "y"}, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	var out map[string]string
	if err := c.Get(ctx, "k", &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if out["x"] != "y" {
		t.Fatalf("round-trip: %+v", out)
	}
	// Get missing
	var miss map[string]string
	if err := c.Get(ctx, "missing", &miss); err == nil {
		t.Fatal("want missing err")
	}
	// Set marshal error
	if err := c.Set(ctx, "bad", make(chan int), time.Minute); err == nil {
		t.Fatal("want marshal err for chan")
	}
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// DeleteByPattern
	for _, k := range []string{"a:1", "a:2", "b:1"} {
		if err := c.Set(ctx, k, "v", time.Minute); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	if err := c.DeleteByPattern(ctx, "a:*"); err != nil {
		t.Fatalf("dbp: %v", err)
	}
}

func TestNewRedisCache_BadAddr(t *testing.T) {
	_, err := NewRedisCache("127.0.0.1:1")
	if err == nil {
		t.Fatal("want err for unreachable")
	}
}
