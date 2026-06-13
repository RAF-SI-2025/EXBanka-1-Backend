package service

import (
	"context"
	"fmt"

	"github.com/exbanka/account-service/internal/cache"
)

// Account cache key formats. Centralized here so the read side (Set/Get) and
// EVERY invalidation (Delete) across AccountService and the reservation services
// use the exact same strings. A drifting format would silently serve a stale
// balance — the failure mode this package guards against.
func accountCacheKeyByID(id uint64) string      { return fmt.Sprintf("account:id:%d", id) }
func accountCacheKeyByNumber(num string) string { return fmt.Sprintf("account:num:%s", num) }

// evictAccountCache drops the cached account by id and/or number after a balance
// mutation. EVERY code path that changes an account's balance / available /
// reserved / spending MUST call this (with the cache it holds) so the next
// GetAccount / GetAccountByNumber read is fresh. No-op when the cache is nil
// (graceful degradation when Redis is down).
func evictAccountCache(c *cache.RedisCache, id uint64, number string) {
	if c == nil {
		return
	}
	ctx := context.Background()
	if id != 0 {
		_ = c.Delete(ctx, accountCacheKeyByID(id))
	}
	if number != "" {
		_ = c.Delete(ctx, accountCacheKeyByNumber(number))
	}
}
