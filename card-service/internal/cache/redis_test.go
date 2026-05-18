package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRedisCache_BadAddrFailsPing(t *testing.T) {
	c, err := NewRedisCache("127.0.0.1:1")
	assert.Error(t, err)
	assert.Nil(t, c)
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
	type p struct {
		K string `json:"k"`
	}
	require.NoError(t, c.Set(context.Background(), "key", p{K: "v"}, time.Minute))
	var got p
	require.NoError(t, c.Get(context.Background(), "key", &got))
	assert.Equal(t, "v", got.K)
}

func TestRedisCache_Get_MissingKey(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()
	var got map[string]any
	require.Error(t, c.Get(context.Background(), "nope", &got))
}

func TestRedisCache_Delete(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()
	require.NoError(t, c.Set(context.Background(), "k", "v", time.Minute))
	require.NoError(t, c.Delete(context.Background(), "k"))
	var got string
	require.Error(t, c.Get(context.Background(), "k", &got))
}

func TestRedisCache_DeleteByPattern(t *testing.T) {
	c, _ := newMiniredisCache(t)
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, c.Set(ctx, "card:1", "v", time.Minute))
	require.NoError(t, c.Set(ctx, "card:2", "v", time.Minute))
	require.NoError(t, c.Set(ctx, "other", "v", time.Minute))
	require.NoError(t, c.DeleteByPattern(ctx, "card:*"))
	var got string
	assert.Error(t, c.Get(ctx, "card:1", &got))
	assert.NoError(t, c.Get(ctx, "other", &got))
}

func TestRedisCache_Close(t *testing.T) {
	c, _ := newMiniredisCache(t)
	require.NoError(t, c.Close())
}
