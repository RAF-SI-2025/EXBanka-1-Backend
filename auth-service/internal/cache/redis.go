package cache

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(addr string) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}
	return &RedisCache{client: client}, nil
}

func (c *RedisCache) Get(ctx context.Context, key string, dest interface{}) error {
	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(val), dest)
}

func (c *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, data, ttl).Err()
}

func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

func (c *RedisCache) DeleteByPattern(ctx context.Context, pattern string) error {
	iter := c.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		c.client.Del(ctx, iter.Val())
	}
	return iter.Err()
}

func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, key).Result()
	return n > 0, err
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}

// SetUserRevokedAt records the revocation epoch for a user. Tokens whose
// `iat` predates this timestamp must be rejected by ValidateToken.
// ttl is the access-token lifetime — after it expires no surviving token
// could possibly be older than the cutoff anyway, so the key is safe to drop.
func (c *RedisCache) SetUserRevokedAt(ctx context.Context, userID int64, atUnix int64, ttl time.Duration) error {
	key := userRevokedAtKey(userID)
	return c.client.Set(ctx, key, atUnix, ttl).Err()
}

// GetUserRevokedAt returns the revocation epoch in unix seconds, or 0 when
// no revocation key exists. A Redis error is propagated so callers can
// decide on fail-open vs fail-closed.
func (c *RedisCache) GetUserRevokedAt(ctx context.Context, userID int64) (int64, error) {
	key := userRevokedAtKey(userID)
	val, err := c.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return val, nil
}

func userRevokedAtKey(userID int64) string {
	return "user_revoked_at:" + strconv.FormatInt(userID, 10)
}
