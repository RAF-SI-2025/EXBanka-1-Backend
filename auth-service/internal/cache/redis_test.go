package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCache spins up an in-process miniredis and returns a cache wired to it.
func newTestCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return newRedisCacheWithClient(client), mr
}

type cacheValue struct {
	Foo string `json:"foo"`
	N   int    `json:"n"`
}

func TestRedisCache_SetAndGet(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	in := cacheValue{Foo: "bar", N: 42}
	require.NoError(t, c.Set(ctx, "key1", in, time.Minute))

	var out cacheValue
	require.NoError(t, c.Get(ctx, "key1", &out))
	assert.Equal(t, in, out)
}

func TestRedisCache_GetMissingKey(t *testing.T) {
	c, _ := newTestCache(t)
	var out cacheValue
	err := c.Get(context.Background(), "missing", &out)
	require.Error(t, err)
	assert.True(t, errors.Is(err, redis.Nil), "expected redis.Nil for missing key, got: %v", err)
}

func TestRedisCache_TTLExpiry(t *testing.T) {
	c, mr := newTestCache(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "expiring", cacheValue{Foo: "x"}, 30*time.Second))

	// Within TTL, key exists.
	exists, err := c.Exists(ctx, "expiring")
	require.NoError(t, err)
	assert.True(t, exists)

	// Fast-forward beyond TTL using miniredis time travel.
	mr.FastForward(31 * time.Second)

	exists, err = c.Exists(ctx, "expiring")
	require.NoError(t, err)
	assert.False(t, exists, "key should expire after TTL")
}

func TestRedisCache_Delete(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "del-me", cacheValue{Foo: "y"}, time.Minute))
	require.NoError(t, c.Delete(ctx, "del-me"))

	exists, err := c.Exists(ctx, "del-me")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRedisCache_ExistsTrue(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()
	require.NoError(t, c.Set(ctx, "present", cacheValue{Foo: "z"}, time.Minute))
	exists, err := c.Exists(ctx, "present")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestRedisCache_ExistsFalse(t *testing.T) {
	c, _ := newTestCache(t)
	exists, err := c.Exists(context.Background(), "nope")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRedisCache_DeleteByPattern(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "session:1", cacheValue{Foo: "a"}, time.Minute))
	require.NoError(t, c.Set(ctx, "session:2", cacheValue{Foo: "b"}, time.Minute))
	require.NoError(t, c.Set(ctx, "other:1", cacheValue{Foo: "c"}, time.Minute))

	require.NoError(t, c.DeleteByPattern(ctx, "session:*"))

	gone1, _ := c.Exists(ctx, "session:1")
	gone2, _ := c.Exists(ctx, "session:2")
	other, _ := c.Exists(ctx, "other:1")

	assert.False(t, gone1)
	assert.False(t, gone2)
	assert.True(t, other, "non-matching keys should be retained")
}

func TestRedisCache_GetUnmarshalError(t *testing.T) {
	c, mr := newTestCache(t)
	// Inject a non-JSON value directly.
	require.NoError(t, mr.Set("badjson", "not-valid-json"))
	var out cacheValue
	err := c.Get(context.Background(), "badjson", &out)
	assert.Error(t, err, "expected unmarshal error for non-JSON value")
}

func TestRedisCache_SetUserRevokedAt_GetUserRevokedAt(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	now := time.Now().Unix()
	require.NoError(t, c.SetUserRevokedAt(ctx, 7, now, time.Minute))

	got, err := c.GetUserRevokedAt(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, now, got)
}

func TestRedisCache_GetUserRevokedAt_NoEntry(t *testing.T) {
	c, _ := newTestCache(t)
	got, err := c.GetUserRevokedAt(context.Background(), 999)
	require.NoError(t, err, "missing key should not error")
	assert.Equal(t, int64(0), got)
}

func TestRedisCache_SetUserRevokedAt_TTLExpires(t *testing.T) {
	c, mr := newTestCache(t)
	ctx := context.Background()

	require.NoError(t, c.SetUserRevokedAt(ctx, 8, 12345, 30*time.Second))
	mr.FastForward(31 * time.Second)
	got, err := c.GetUserRevokedAt(ctx, 8)
	require.NoError(t, err)
	assert.Equal(t, int64(0), got, "expired user_revoked_at should read as zero")
}

func TestRedisCache_Close(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	c := newRedisCacheWithClient(client)
	assert.NoError(t, c.Close())
}

func TestUserRevokedAtKey_Format(t *testing.T) {
	assert.Equal(t, "user_revoked_at:42", userRevokedAtKey(42))
	assert.Equal(t, "user_revoked_at:0", userRevokedAtKey(0))
}
