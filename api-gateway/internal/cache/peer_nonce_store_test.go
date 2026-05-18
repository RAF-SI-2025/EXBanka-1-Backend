package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/exbanka/api-gateway/internal/cache"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*cache.PeerNonceStore, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := cache.NewPeerNonceStore(rdb, 10*time.Minute)
	return store, mr.Close
}

func TestPeerNonceStore_FirstSeenIsNew(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ok, err := store.Claim(context.Background(), "222", "n-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Errorf("expected fresh nonce to claim")
	}
}

func TestPeerNonceStore_DuplicateIsRejected(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	if _, err := store.Claim(context.Background(), "222", "n-1"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.Claim(context.Background(), "222", "n-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ok {
		t.Errorf("expected duplicate nonce to be rejected")
	}
}

func TestPeerNonceStore_DifferentBankCodesIndependent(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	if _, err := store.Claim(context.Background(), "222", "n-1"); err != nil {
		t.Fatal(err)
	}
	ok, _ := store.Claim(context.Background(), "333", "n-1")
	if !ok {
		t.Errorf("nonces should be scoped per bank code")
	}
}
