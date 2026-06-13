package shared

import (
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

func TestWithIdempotencyKey_GeneratesWhenAbsent(t *testing.T) {
	m := withIdempotencyKey(kafkago.Message{Topic: "t", Value: []byte("x")})
	if got := IdempotencyKeyFromHeaders(m.Headers); got == "" {
		t.Fatal("expected a generated idempotency key header")
	}
}

func TestWithIdempotencyKey_PreservesCallerKey(t *testing.T) {
	pre := kafkago.Message{
		Topic:   "t",
		Headers: []kafkago.Header{{Key: HeaderIdempotencyKey, Value: []byte("mine")}},
	}
	got := withIdempotencyKey(pre)
	if k := IdempotencyKeyFromHeaders(got.Headers); k != "mine" {
		t.Fatalf("caller-set key must be preserved, got %q", k)
	}
	count := 0
	for _, h := range got.Headers {
		if h.Key == HeaderIdempotencyKey {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 idempotency header, got %d", count)
	}
}

func TestIdempotencyKeyFromHeaders_Absent(t *testing.T) {
	if got := IdempotencyKeyFromHeaders(nil); got != "" {
		t.Fatalf("expected empty string for no headers, got %q", got)
	}
}
