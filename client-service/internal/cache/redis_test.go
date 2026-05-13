package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRedisCache_BadAddrFailsPing exercises the construction failure
// branch: the Ping inside NewRedisCache must error when the address is
// unreachable.
func TestNewRedisCache_BadAddrFailsPing(t *testing.T) {
	// 127.0.0.1:1 is reserved/unused; Ping will fail quickly with a
	// connection-refused or timeout, exercising the err-return branch.
	c, err := NewRedisCache("127.0.0.1:1")
	assert.Error(t, err)
	assert.Nil(t, c, "no cache returned on Ping failure")
}

func newMiniredisCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := NewRedisCache(mr.Addr())
	require.NoError(t, err)
	return c, mr
}

func TestRedisCache_SetThenGet(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()

	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	require.NoError(t, c.Set(context.Background(), "k", payload{Name: "x", Count: 7}, time.Minute))

	var got payload
	require.NoError(t, c.Get(context.Background(), "k", &got))
	assert.Equal(t, "x", got.Name)
	assert.Equal(t, 7, got.Count)
}

func TestRedisCache_Get_MissingKey(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()

	var got map[string]any
	err := c.Get(context.Background(), "no-such-key", &got)
	require.Error(t, err, "Get on missing key returns error")
}

func TestRedisCache_Delete(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()
	require.NoError(t, c.Set(context.Background(), "k1", "value", time.Minute))

	require.NoError(t, c.Delete(context.Background(), "k1"))

	var got string
	require.Error(t, c.Get(context.Background(), "k1", &got))
}

func TestRedisCache_DeleteByPattern(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "client:1", "v1", time.Minute))
	require.NoError(t, c.Set(ctx, "client:2", "v2", time.Minute))
	require.NoError(t, c.Set(ctx, "other:1", "v3", time.Minute))

	require.NoError(t, c.DeleteByPattern(ctx, "client:*"))

	var got string
	assert.Error(t, c.Get(ctx, "client:1", &got))
	assert.Error(t, c.Get(ctx, "client:2", &got))
	assert.NoError(t, c.Get(ctx, "other:1", &got), "non-matching key must remain")
}

func TestRedisCache_Close(t *testing.T) {
	c, _ := newMiniredisCache(t)
	require.NoError(t, c.Close())
}
