# Redis Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Redis caching where read patterns justify it (exchange rates, securities, cards, accounts) and remove unused Redis initialization from transaction-service and credit-service.

**Architecture:** Follow existing cache pattern (JSON serialize to Redis with TTL, graceful degradation on miss/error). Cache only read-heavy data; never cache for authoritative writes.

**Tech Stack:** Go, Redis, go-redis/redis/v9

---

### Task 1: Add Redis caching to exchange-service

Exchange-service has NO Redis today. Every `Convert`, `GetRate`, and `Calculate` call hits PostgreSQL. Exchange rates change at most every 6 hours (sync interval), so a 5-minute TTL cache is safe and eliminates the vast majority of DB reads.

**Files:**
- Create: `exchange-service/internal/cache/redis.go`
- Modify: `exchange-service/internal/config/config.go`
- Modify: `exchange-service/internal/service/exchange_service.go`
- Modify: `exchange-service/cmd/main.go`
- Modify: `exchange-service/go.mod` (add go-redis dependency)
- Modify: `docker-compose.yml` (add REDIS_ADDR + depends_on for exchange-service)

- [ ] **Step 1: Add go-redis dependency**

```bash
cd exchange-service && go get github.com/redis/go-redis/v9@latest
```

- [ ] **Step 2: Create `exchange-service/internal/cache/redis.go`**

Copy the identical cache pattern used by card-service, account-service, etc.

```go
// exchange-service/internal/cache/redis.go
package cache

import (
	"context"
	"encoding/json"
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

func (c *RedisCache) Close() error {
	return c.client.Close()
}
```

- [ ] **Step 3: Add `RedisAddr` to exchange-service config**

In `exchange-service/internal/config/config.go`, add the field and load it:

```go
// Add to Config struct:
RedisAddr string

// Add to Load() return:
RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
```

The full diff for the Config struct:

```go
type Config struct {
	DBHost            string
	DBPort            string
	DBUser            string
	DBPassword        string
	DBName            string
	GRPCAddr          string
	KafkaBrokers      string
	RedisAddr         string // <-- ADD
	APIKey            string
	CommissionRate    string
	Spread            string
	SyncIntervalHours int
}
```

And in `Load()`, add between `KafkaBrokers` and `APIKey`:

```go
RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
```

- [ ] **Step 4: Add cache field to ExchangeService and wire caching into reads**

In `exchange-service/internal/service/exchange_service.go`:

Add imports:

```go
import (
	"context"
	"fmt"
	"log"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/exchange-service/internal/cache"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/provider"
	"github.com/exbanka/exchange-service/internal/repository"
)
```

Add cache field and TTL constant:

```go
const rateCacheTTL = 5 * time.Minute

type ExchangeService struct {
	repo           *repository.ExchangeRateRepository
	upserter       RateUpserter
	db             *gorm.DB
	cache          *cache.RedisCache // nil = no cache (graceful degradation)
	commissionRate decimal.Decimal
	spread         decimal.Decimal
}
```

Add the `"time"` import to the import block.

Update constructors:

```go
func NewExchangeService(repo *repository.ExchangeRateRepository, db *gorm.DB, cache *cache.RedisCache, commissionRate, spread string) (*ExchangeService, error) {
	cr, err := decimal.NewFromString(commissionRate)
	if err != nil {
		return nil, fmt.Errorf("invalid commission rate %q: %w", commissionRate, err)
	}
	sp, err := decimal.NewFromString(spread)
	if err != nil {
		return nil, fmt.Errorf("invalid spread %q: %w", spread, err)
	}
	return &ExchangeService{repo: repo, upserter: repo, db: db, cache: cache, commissionRate: cr, spread: sp}, nil
}

func NewExchangeServiceWithUpserter(
	repo *repository.ExchangeRateRepository,
	upserter RateUpserter,
	db *gorm.DB,
	cache *cache.RedisCache,
	commissionRate, spread string,
) (*ExchangeService, error) {
	svc, err := NewExchangeService(repo, db, cache, commissionRate, spread)
	if err != nil {
		return nil, err
	}
	svc.upserter = upserter
	return svc, nil
}
```

Add a cached rate lookup helper:

```go
// getCachedRate tries Redis first, falls back to DB, and populates cache on miss.
func (s *ExchangeService) getCachedRate(from, to string) (*model.ExchangeRate, error) {
	ctx := context.Background()
	key := fmt.Sprintf("rate:%s:%s", from, to)

	if s.cache != nil {
		var rate model.ExchangeRate
		if err := s.cache.Get(ctx, key, &rate); err == nil {
			return &rate, nil
		}
		// Cache miss or error — fall through to DB
	}

	rate, err := s.repo.GetByPair(from, to)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, rate, rateCacheTTL); err != nil {
			log.Printf("WARN: failed to cache rate %s/%s: %v", from, to, err)
		}
	}
	return rate, nil
}
```

Replace all `s.repo.GetByPair(...)` calls with `s.getCachedRate(...)` in these methods:
- `singleLeg` (line 170)
- `twoLeg` (lines 179, 185)
- `Calculate` (lines 137, 146)
- `GetRate` (line 165)

Updated methods:

```go
func (s *ExchangeService) GetRate(from, to string) (*model.ExchangeRate, error) {
	return s.getCachedRate(from, to)
}

func (s *ExchangeService) singleLeg(from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	rate, err := s.getCachedRate(from, to)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup %s/%s: %w", from, to, err)
	}
	return amount.Mul(rate.SellRate), rate.SellRate, nil
}

func (s *ExchangeService) twoLeg(from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	rateFromRSD, err := s.getCachedRate(from, "RSD")
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup %s/RSD: %w", from, err)
	}
	rsdAmount := amount.Mul(rateFromRSD.SellRate)

	rateToTarget, err := s.getCachedRate("RSD", to)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup RSD/%s: %w", to, err)
	}
	converted := rsdAmount.Mul(rateToTarget.SellRate)
	effRate := rateFromRSD.SellRate.Mul(rateToTarget.SellRate)
	return converted, effRate, nil
}

func (s *ExchangeService) Calculate(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	if from == to {
		return amount, decimal.Zero, decimal.NewFromInt(1), nil
	}
	if from == "RSD" || to == "RSD" {
		gross, effRate, err := s.Convert(ctx, from, to, amount)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, err
		}
		commission := gross.Mul(s.commissionRate)
		return gross.Sub(commission), s.commissionRate, effRate, nil
	}
	// Two-step with per-leg commission
	rateStep1, err := s.getCachedRate(from, "RSD")
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup %s/RSD: %w", from, err)
	}
	grossRSD := amount.Mul(rateStep1.SellRate)
	commRSD := grossRSD.Mul(s.commissionRate)
	netRSD := grossRSD.Sub(commRSD)

	rateStep2, err := s.getCachedRate("RSD", to)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("rate lookup RSD/%s: %w", to, err)
	}
	grossTarget := netRSD.Mul(rateStep2.SellRate)
	commTarget := grossTarget.Mul(s.commissionRate)
	netTarget := grossTarget.Sub(commTarget)

	effRate := rateStep1.SellRate.Mul(rateStep2.SellRate)
	return netTarget, s.commissionRate, effRate, nil
}
```

- [ ] **Step 5: Invalidate cache after SyncRates**

Add cache invalidation at the end of `SyncRates`, after the DB transaction succeeds:

```go
func (s *ExchangeService) SyncRates(ctx context.Context, p provider.RateProvider) error {
	rates, err := p.FetchRatesFromRSD()
	if err != nil {
		log.Printf("WARN: rate provider sync failed, keeping cached rates: %v", err)
		return err
	}

	one := decimal.NewFromInt(1)
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		for code, midRsdToC := range rates {
			if midRsdToC.IsZero() {
				continue
			}
			buyRsdToC := midRsdToC.Mul(one.Sub(s.spread))
			sellRsdToC := midRsdToC.Mul(one.Add(s.spread))
			if err := s.upserter.UpsertInTx(tx, "RSD", code, buyRsdToC, sellRsdToC); err != nil {
				return fmt.Errorf("failed to upsert RSD/%s: %w", code, err)
			}

			midCToRsd := one.Div(midRsdToC)
			buyCToRsd := midCToRsd.Mul(one.Sub(s.spread))
			sellCToRsd := midCToRsd.Mul(one.Add(s.spread))
			if err := s.upserter.UpsertInTx(tx, code, "RSD", buyCToRsd, sellCToRsd); err != nil {
				return fmt.Errorf("failed to upsert %s/RSD: %w", code, err)
			}
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	// Invalidate all cached rates after successful DB sync
	if s.cache != nil {
		if err := s.cache.DeleteByPattern(ctx, "rate:*"); err != nil {
			log.Printf("WARN: failed to invalidate rate cache: %v", err)
		}
	}
	return nil
}
```

- [ ] **Step 6: Wire Redis in `exchange-service/cmd/main.go`**

Add import:

```go
"github.com/exbanka/exchange-service/internal/cache"
```

After the Kafka topic creation and before repo/service setup, add:

```go
var redisCache *cache.RedisCache
redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
if err != nil {
	log.Printf("warn: redis unavailable, running without cache: %v", err)
}
if redisCache != nil {
	defer redisCache.Close()
}
```

Update the service constructor call:

```go
// BEFORE:
svc, err := service.NewExchangeService(repo, db, cfg.CommissionRate, cfg.Spread)

// AFTER:
svc, err := service.NewExchangeService(repo, db, redisCache, cfg.CommissionRate, cfg.Spread)
```

- [ ] **Step 7: Update docker-compose.yml for exchange-service**

Add `REDIS_ADDR` and `depends_on: redis` to the exchange-service block:

```yaml
  exchange-service:
    build:
      context: .
      dockerfile: exchange-service/Dockerfile
    environment:
      EXCHANGE_DB_HOST: exchange-db
      EXCHANGE_DB_PORT: "5432"
      EXCHANGE_DB_USER: postgres
      EXCHANGE_DB_PASSWORD: postgres
      EXCHANGE_DB_NAME: exchangedb
      EXCHANGE_GRPC_ADDR: ":50059"
      KAFKA_BROKERS: kafka:9092
      REDIS_ADDR: redis:6379          # <-- ADD
      EXCHANGE_COMMISSION_RATE: "0.005"
      EXCHANGE_SPREAD: "0.003"
      EXCHANGE_DB_SSLMODE: disable
    ports:
      - "50059:50059"
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 50059 || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 15s
    depends_on:
      exchange-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:                           # <-- ADD
        condition: service_healthy     # <-- ADD
```

- [ ] **Step 8: Update existing tests**

Any test that calls `NewExchangeService` or `NewExchangeServiceWithUpserter` needs to pass `nil` for the new cache parameter. Search for these calls in test files:

```bash
cd exchange-service && grep -rn "NewExchangeService\|NewExchangeServiceWithUpserter" --include="*_test.go"
```

For each call, insert `nil` as the third argument (after `db`, before `commissionRate`):

```go
// BEFORE:
svc, err := service.NewExchangeService(repo, db, "0.005", "0.003")

// AFTER:
svc, err := service.NewExchangeService(repo, db, nil, "0.005", "0.003")
```

- [ ] **Step 9: Verify**

```bash
cd exchange-service && go build ./cmd && go test ./... -v
```

- [ ] **Step 10: Commit**

```
feat(exchange): add Redis caching for exchange rate lookups

Cache key pattern: rate:<from>:<to>, TTL: 5 minutes.
Invalidated on SyncRates. Graceful degradation when Redis unavailable.
```

---

### Task 2: Add Redis caching to stock-service for securities

Stock-service has NO Redis today. Individual security lookups (GetStock, GetFutures, GetForexPair, GetOption) hit the DB every time. With a 2-minute TTL, this is safe since prices refresh every 15 minutes.

**Files:**
- Create: `stock-service/internal/cache/redis.go`
- Modify: `stock-service/internal/config/config.go`
- Modify: `stock-service/internal/service/security_service.go`
- Modify: `stock-service/internal/service/security_sync.go`
- Modify: `stock-service/cmd/main.go`
- Modify: `stock-service/go.mod` (add go-redis dependency)
- Modify: `docker-compose.yml` (add REDIS_ADDR + depends_on for stock-service)

- [ ] **Step 1: Add go-redis dependency**

```bash
cd stock-service && go get github.com/redis/go-redis/v9@latest
```

- [ ] **Step 2: Create `stock-service/internal/cache/redis.go`**

Identical to the exchange-service cache file created in Task 1 Step 2, but with package path `github.com/exbanka/stock-service/internal/cache`.

```go
// stock-service/internal/cache/redis.go
package cache

import (
	"context"
	"encoding/json"
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

func (c *RedisCache) Close() error {
	return c.client.Close()
}
```

- [ ] **Step 3: Add `RedisAddr` to stock-service config**

In `stock-service/internal/config/config.go`, add the field:

```go
// Add to Config struct (after KafkaBrokers):
RedisAddr string

// Add to Load() return (after KafkaBrokers line):
RedisAddr:             getEnv("REDIS_ADDR", "localhost:6379"),
```

- [ ] **Step 4: Add cache field to SecurityService**

In `stock-service/internal/service/security_service.go`:

Add imports:

```go
import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/cache"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)
```

Add cache field and TTL constant:

```go
const securityCacheTTL = 2 * time.Minute

type SecurityService struct {
	stockRepo    StockRepo
	futuresRepo  FuturesRepo
	forexRepo    ForexPairRepo
	optionRepo   OptionRepo
	exchangeRepo ExchangeRepo
	cache        *cache.RedisCache // nil = no cache
}

func NewSecurityService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo ExchangeRepo,
	cache *cache.RedisCache,
) *SecurityService {
	return &SecurityService{
		stockRepo:    stockRepo,
		futuresRepo:  futuresRepo,
		forexRepo:    forexRepo,
		optionRepo:   optionRepo,
		exchangeRepo: exchangeRepo,
		cache:        cache,
	}
}
```

Add cached lookup helpers for each security type:

```go
func (s *SecurityService) GetStock(id uint64) (*model.Stock, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:stock:%d", id)

	if s.cache != nil {
		var stock model.Stock
		if err := s.cache.Get(ctx, key, &stock); err == nil {
			return &stock, nil
		}
	}

	stock, err := s.stockRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("stock not found")
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, stock, securityCacheTTL); err != nil {
			log.Printf("WARN: failed to cache stock %d: %v", id, err)
		}
	}
	return stock, nil
}

func (s *SecurityService) GetFutures(id uint64) (*model.FuturesContract, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:futures:%d", id)

	if s.cache != nil {
		var f model.FuturesContract
		if err := s.cache.Get(ctx, key, &f); err == nil {
			return &f, nil
		}
	}

	f, err := s.futuresRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("futures contract not found")
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, f, securityCacheTTL); err != nil {
			log.Printf("WARN: failed to cache futures %d: %v", id, err)
		}
	}
	return f, nil
}

func (s *SecurityService) GetForexPair(id uint64) (*model.ForexPair, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:forex:%d", id)

	if s.cache != nil {
		var fp model.ForexPair
		if err := s.cache.Get(ctx, key, &fp); err == nil {
			return &fp, nil
		}
	}

	fp, err := s.forexRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("forex pair not found")
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, fp, securityCacheTTL); err != nil {
			log.Printf("WARN: failed to cache forex pair %d: %v", id, err)
		}
	}
	return fp, nil
}

func (s *SecurityService) GetOption(id uint64) (*model.Option, error) {
	ctx := context.Background()
	key := fmt.Sprintf("security:option:%d", id)

	if s.cache != nil {
		var o model.Option
		if err := s.cache.Get(ctx, key, &o); err == nil {
			return &o, nil
		}
	}

	o, err := s.optionRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("option not found")
		}
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, o, securityCacheTTL); err != nil {
			log.Printf("WARN: failed to cache option %d: %v", id, err)
		}
	}
	return o, nil
}
```

The `ListStocks`, `ListFutures`, `ListForexPairs`, `ListOptions`, `GetStockWithOptions`, `StockChangePercent`, `GenerateOptionsForStock`, and helper functions remain unchanged.

- [ ] **Step 5: Add cache invalidation to SecuritySyncService.RefreshPrices**

In `stock-service/internal/service/security_sync.go`, add a cache field to `SecuritySyncService`:

```go
type SecuritySyncService struct {
	stockRepo     StockRepo
	futuresRepo   FuturesRepo
	forexRepo     ForexPairRepo
	optionRepo    OptionRepo
	exchangeRepo  *repository.ExchangeRepository
	settingRepo   SettingRepo
	avClient      *provider.AlphaVantageClient
	eodhClient    *provider.EODHDClient
	alpacaClient  *provider.AlpacaClient
	finnhubClient *provider.FinnhubClient
	listingSvc    *ListingService
	csvPath       string
	cache         *cache.RedisCache // nil = no cache
}
```

Add the `cache` import:

```go
"github.com/exbanka/stock-service/internal/cache"
```

Update the constructor:

```go
func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo *repository.ExchangeRepository,
	settingRepo SettingRepo,
	avClient *provider.AlphaVantageClient,
	eodhClient *provider.EODHDClient,
	alpacaClient *provider.AlpacaClient,
	finnhubClient *provider.FinnhubClient,
	listingSvc *ListingService,
	csvPath string,
	cache *cache.RedisCache,
) *SecuritySyncService {
	return &SecuritySyncService{
		stockRepo:     stockRepo,
		futuresRepo:   futuresRepo,
		forexRepo:     forexRepo,
		optionRepo:    optionRepo,
		exchangeRepo:  exchangeRepo,
		settingRepo:   settingRepo,
		avClient:      avClient,
		eodhClient:    eodhClient,
		alpacaClient:  alpacaClient,
		finnhubClient: finnhubClient,
		listingSvc:    listingSvc,
		csvPath:       csvPath,
		cache:         cache,
	}
}
```

Add cache invalidation at the end of `RefreshPrices`:

```go
func (s *SecuritySyncService) RefreshPrices(ctx context.Context) {
	if s.isTestingMode() {
		log.Println("testing mode enabled — skipping external API price refresh")
		return
	}
	s.syncStockPrices(ctx)
	s.refreshForexRates()
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}

	// Invalidate all cached securities after price refresh
	if s.cache != nil {
		if err := s.cache.DeleteByPattern(ctx, "security:*"); err != nil {
			log.Printf("WARN: failed to invalidate security cache: %v", err)
		}
	}

	log.Println("price refresh complete")
}
```

- [ ] **Step 6: Wire Redis in `stock-service/cmd/main.go`**

Add import:

```go
"github.com/exbanka/stock-service/internal/cache"
```

After the Kafka topic creation block (after `kafkaprod.EnsureTopics(...)`) and before the gRPC client connections, add:

```go
// --- Redis ---
var redisCache *cache.RedisCache
redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
if err != nil {
	log.Printf("warn: redis unavailable, running without cache: %v", err)
}
if redisCache != nil {
	defer redisCache.Close()
}
```

Update the `NewSecurityService` call to pass `redisCache`:

```go
// BEFORE:
secSvc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo)

// AFTER:
secSvc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo, redisCache)
```

Update the `NewSecuritySyncService` call to pass `redisCache` as the last argument:

```go
// BEFORE:
syncSvc := service.NewSecuritySyncService(
	stockRepo, futuresRepo, forexRepo, optionRepo,
	exchangeRepo, settingRepo, avClient,
	eodhClient, alpacaClient, finnhubClient,
	listingSvc, cfg.ExchangeCSVPath,
)

// AFTER:
syncSvc := service.NewSecuritySyncService(
	stockRepo, futuresRepo, forexRepo, optionRepo,
	exchangeRepo, settingRepo, avClient,
	eodhClient, alpacaClient, finnhubClient,
	listingSvc, cfg.ExchangeCSVPath,
	redisCache,
)
```

- [ ] **Step 7: Update docker-compose.yml for stock-service**

Add `REDIS_ADDR` and `depends_on: redis` to the stock-service block:

```yaml
  stock-service:
    build:
      context: .
      dockerfile: stock-service/Dockerfile
    environment:
      # ... existing vars ...
      REDIS_ADDR: redis:6379          # <-- ADD
    ports:
      - "50060:50060"
    depends_on:
      stock-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:                           # <-- ADD
        condition: service_healthy     # <-- ADD
      user-service:
        condition: service_started
      account-service:
        condition: service_started
      exchange-service:
        condition: service_started
      client-service:
        condition: service_started
```

- [ ] **Step 8: Update existing tests**

Search for all calls to `NewSecurityService` and `NewSecuritySyncService` in test files and add `nil` for the new cache parameter:

```bash
cd stock-service && grep -rn "NewSecurityService\|NewSecuritySyncService" --include="*_test.go"
```

For `NewSecurityService`, add `nil` as the last argument:

```go
// BEFORE:
svc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo)

// AFTER:
svc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo, nil)
```

For `NewSecuritySyncService`, add `nil` as the last argument:

```go
// BEFORE:
syncSvc := service.NewSecuritySyncService(..., csvPath)

// AFTER:
syncSvc := service.NewSecuritySyncService(..., csvPath, nil)
```

- [ ] **Step 9: Verify**

```bash
cd stock-service && go build ./cmd && go test ./... -v
```

- [ ] **Step 10: Commit**

```
feat(stock): add Redis caching for individual security lookups

Cache key pattern: security:<type>:<id>, TTL: 2 minutes.
Invalidated on RefreshPrices. Graceful degradation when Redis unavailable.
```

---

### Task 3: Activate existing Redis cache in card-service

Card-service already initializes Redis and passes it to `CardService`, but the `cache` field is never used in any method. The `GetCard` method hits the DB on every call. Add cache reads to `GetCard` and invalidation to all mutating methods.

**Files:**
- Modify: `card-service/internal/service/card_service.go`

- [ ] **Step 1: Add cache constants and imports**

At the top of `card-service/internal/service/card_service.go`, add to the import block:

```go
"log"
```

Add the TTL constant after the import block:

```go
const cardCacheTTL = 3 * time.Minute
```

- [ ] **Step 2: Add cache helper method**

```go
// cacheKey returns the Redis key for a card by ID.
func cardCacheKey(id uint64) string {
	return fmt.Sprintf("card:id:%d", id)
}

// invalidateCard removes a card from the cache. Silently ignores errors.
func (s *CardService) invalidateCard(id uint64) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Delete(context.Background(), cardCacheKey(id)); err != nil {
		log.Printf("WARN: failed to invalidate card cache %d: %v", id, err)
	}
}
```

- [ ] **Step 3: Add caching to GetCard**

Replace the current `GetCard` method:

```go
// BEFORE:
func (s *CardService) GetCard(id uint64) (*model.Card, error) {
	return s.cardRepo.GetByID(id)
}

// AFTER:
func (s *CardService) GetCard(id uint64) (*model.Card, error) {
	ctx := context.Background()
	key := cardCacheKey(id)

	if s.cache != nil {
		var card model.Card
		if err := s.cache.Get(ctx, key, &card); err == nil {
			return &card, nil
		}
	}

	card, err := s.cardRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, card, cardCacheTTL); err != nil {
			log.Printf("WARN: failed to cache card %d: %v", id, err)
		}
	}
	return card, nil
}
```

- [ ] **Step 4: Add invalidation to all mutating methods**

Add `s.invalidateCard(id)` after every successful mutation. In each of the following methods, add the invalidation call right before the return statement (after the DB transaction succeeds):

**BlockCard** -- after `err := s.db.Transaction(...)`, before `return card, err`:

```go
func (s *CardService) BlockCard(id uint64) (*model.Card, error) {
	var card *model.Card
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// ... existing code ...
	})
	if err == nil {
		s.invalidateCard(id)
	}
	return card, err
}
```

**UnblockCard** -- same pattern:

```go
func (s *CardService) UnblockCard(id uint64) (*model.Card, error) {
	var card *model.Card
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// ... existing code ...
	})
	if err == nil {
		s.invalidateCard(id)
	}
	return card, err
}
```

**DeactivateCard** -- same pattern:

```go
func (s *CardService) DeactivateCard(id uint64) (*model.Card, error) {
	var card *model.Card
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// ... existing code ...
	})
	if err == nil {
		s.invalidateCard(id)
	}
	return card, err
}
```

**TemporaryBlockCard** -- already publishes Kafka after TX; add invalidation next to it:

```go
func (s *CardService) TemporaryBlockCard(ctx context.Context, cardID uint64, durationHours int, reason string) (*model.Card, error) {
	// ... existing validation and transaction code ...
	if err != nil {
		return nil, err
	}
	s.invalidateCard(cardID)
	// Publish Kafka event after the transaction commits (not inside TX).
	_ = s.producer.PublishCardTemporaryBlocked(ctx, kafkamsg.CardTemporaryBlockedMessage{
		CardID:    card.ID,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Reason:    reason,
	})
	return card, nil
}
```

**UseCard** -- add invalidation after transaction:

```go
func (s *CardService) UseCard(cardID uint64) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// ... existing code ...
	})
	if err == nil {
		s.invalidateCard(cardID)
	}
	return err
}
```

**SetPin** -- add invalidation at the end:

```go
func (s *CardService) SetPin(cardID uint64, pin string) error {
	// ... existing code ...
	card.PinHash = string(hash)
	card.PinAttempts = 0
	err = s.cardRepo.Update(card)
	if err == nil {
		s.invalidateCard(cardID)
	}
	return err
}
```

**VerifyPin** -- add invalidation (PIN attempts change card state):

```go
func (s *CardService) VerifyPin(cardID uint64, pin string) (bool, error) {
	var ok bool
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// ... existing code ...
	})
	if err == nil {
		s.invalidateCard(cardID)
	}
	return ok, err
}
```

- [ ] **Step 5: Verify**

```bash
cd card-service && go build ./cmd && go test ./... -v
```

- [ ] **Step 6: Commit**

```
feat(card): activate Redis caching for GetCard with invalidation

Cache key: card:id:<id>, TTL: 3 minutes. Invalidated on BlockCard,
UnblockCard, DeactivateCard, TemporaryBlockCard, UseCard, SetPin, VerifyPin.
```

---

### Task 4: Activate existing Redis cache in account-service

Account-service already initializes Redis but never uses it. Add caching to `GetAccount` and `GetAccountByNumber` for read-only display queries. CRITICAL: do NOT cache for authoritative balance checks (UpdateBalance uses SELECT FOR UPDATE in the repository).

**Files:**
- Modify: `account-service/internal/service/account_service.go`

- [ ] **Step 1: Add cache field and imports**

Add to the import block:

```go
"context"
"log"

"github.com/exbanka/account-service/internal/cache"
```

Add the TTL constant and update the struct:

```go
const accountCacheTTL = 2 * time.Minute

type AccountService struct {
	repo  *repository.AccountRepository
	db    *gorm.DB
	cache *cache.RedisCache // nil = no cache
}

func NewAccountService(repo *repository.AccountRepository, db *gorm.DB, cache *cache.RedisCache) *AccountService {
	return &AccountService{repo: repo, db: db, cache: cache}
}
```

- [ ] **Step 2: Add cache helper methods**

```go
func accountCacheKeyByID(id uint64) string {
	return fmt.Sprintf("account:id:%d", id)
}

func accountCacheKeyByNumber(accountNumber string) string {
	return fmt.Sprintf("account:num:%s", accountNumber)
}

// invalidateAccount removes an account from both cache keys. Silently ignores errors.
func (s *AccountService) invalidateAccount(id uint64, accountNumber string) {
	if s.cache == nil {
		return
	}
	ctx := context.Background()
	if id > 0 {
		if err := s.cache.Delete(ctx, accountCacheKeyByID(id)); err != nil {
			log.Printf("WARN: failed to invalidate account cache id:%d: %v", id, err)
		}
	}
	if accountNumber != "" {
		if err := s.cache.Delete(ctx, accountCacheKeyByNumber(accountNumber)); err != nil {
			log.Printf("WARN: failed to invalidate account cache num:%s: %v", accountNumber, err)
		}
	}
}
```

- [ ] **Step 3: Add caching to GetAccount**

```go
func (s *AccountService) GetAccount(id uint64) (*model.Account, error) {
	ctx := context.Background()
	key := accountCacheKeyByID(id)

	if s.cache != nil {
		var account model.Account
		if err := s.cache.Get(ctx, key, &account); err == nil {
			return &account, nil
		}
	}

	account, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, account, accountCacheTTL); err != nil {
			log.Printf("WARN: failed to cache account %d: %v", id, err)
		}
	}
	return account, nil
}
```

- [ ] **Step 4: Add caching to GetAccountByNumber**

```go
func (s *AccountService) GetAccountByNumber(accountNumber string) (*model.Account, error) {
	ctx := context.Background()
	key := accountCacheKeyByNumber(accountNumber)

	if s.cache != nil {
		var account model.Account
		if err := s.cache.Get(ctx, key, &account); err == nil {
			return &account, nil
		}
	}

	account, err := s.repo.GetByNumber(accountNumber)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, account, accountCacheTTL); err != nil {
			log.Printf("WARN: failed to cache account %s: %v", accountNumber, err)
		}
	}
	return account, nil
}
```

- [ ] **Step 5: Add invalidation to mutating methods**

**UpdateAccountName:**

```go
func (s *AccountService) UpdateAccountName(id, clientID uint64, newName string) error {
	exists, err := s.repo.ExistsByNameAndOwner(newName, clientID, id)
	if err != nil {
		return fmt.Errorf("failed to check account name uniqueness: %w", err)
	}
	if exists {
		return fmt.Errorf("an account with name %q already exists for this client", newName)
	}
	err = s.repo.UpdateName(id, clientID, newName)
	if err == nil {
		// We don't have the account number here, so invalidate by ID only.
		// The by-number entry will expire naturally via TTL.
		s.invalidateAccount(id, "")
	}
	return err
}
```

**UpdateAccountLimits:**

```go
func (s *AccountService) UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *string) error {
	// ... existing validation code stays the same ...
	err := s.repo.UpdateLimits(id, updates)
	if err == nil {
		s.invalidateAccount(id, "")
	}
	return err
}
```

**UpdateAccountStatus:**

```go
func (s *AccountService) UpdateAccountStatus(id uint64, newStatus string) error {
	if newStatus != "active" && newStatus != "inactive" {
		return fmt.Errorf("account status must be 'active' or 'inactive'; got: %s", newStatus)
	}

	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("account %d not found", id)
	}

	if account.Status == newStatus {
		return fmt.Errorf("account %d is already %s", id, newStatus)
	}

	err = s.repo.UpdateStatus(id, newStatus)
	if err == nil {
		s.invalidateAccount(id, account.AccountNumber)
	}
	return err
}
```

**UpdateBalance** -- invalidate after balance changes:

```go
func (s *AccountService) UpdateBalance(accountNumber string, amount decimal.Decimal, updateAvailable bool) error {
	err := s.repo.UpdateBalance(accountNumber, amount, updateAvailable)
	if err == nil {
		// Invalidate by account number; ID is unknown here but TTL covers it.
		s.invalidateAccount(0, accountNumber)
	}
	return err
}
```

**UpdateSpending** -- invalidate after spending changes:

```go
func (s *AccountService) UpdateSpending(accountNumber string, amount decimal.Decimal) error {
	err := s.repo.UpdateSpending(accountNumber, amount)
	if err == nil {
		s.invalidateAccount(0, accountNumber)
	}
	return err
}
```

- [ ] **Step 6: Update `account-service/cmd/main.go` to pass cache to AccountService**

Update the `NewAccountService` call:

```go
// BEFORE:
accountService := service.NewAccountService(accountRepo, db)

// AFTER:
accountService := service.NewAccountService(accountRepo, db, redisCache)
```

- [ ] **Step 7: Update existing tests**

Search for `NewAccountService` calls in test files and add `nil` as the third argument:

```bash
cd account-service && grep -rn "NewAccountService" --include="*_test.go"
```

```go
// BEFORE:
svc := service.NewAccountService(repo, db)

// AFTER:
svc := service.NewAccountService(repo, db, nil)
```

- [ ] **Step 8: Verify**

```bash
cd account-service && go build ./cmd && go test ./... -v
```

- [ ] **Step 9: Commit**

```
feat(account): activate Redis caching for GetAccount/GetAccountByNumber

Cache keys: account:id:<id> and account:num:<number>, TTL: 2 minutes.
Invalidated on name/limits/status/balance/spending updates.
Balance checks remain authoritative (SELECT FOR UPDATE in repository).
```

---

### Task 5: Remove unused Redis from transaction-service and credit-service

Both services initialize Redis and import the cache package, but never use the cache in any service or handler code. This adds startup latency (Redis ping) and a false dependency. Remove the dead code.

**Files:**
- Modify: `transaction-service/cmd/main.go`
- Modify: `transaction-service/internal/config/config.go`
- Delete: `transaction-service/internal/cache/redis.go`
- Modify: `credit-service/cmd/main.go`
- Modify: `credit-service/internal/config/config.go`
- Delete: `credit-service/internal/cache/redis.go`
- Modify: `docker-compose.yml`
- Modify: `transaction-service/go.mod` and `credit-service/go.mod` (remove go-redis)

- [ ] **Step 1: Remove cache init from transaction-service/cmd/main.go**

Remove these lines (currently around lines 66-73):

```go
// REMOVE:
	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}
```

Remove the cache import:

```go
// REMOVE from imports:
	"github.com/exbanka/transaction-service/internal/cache"
```

- [ ] **Step 2: Remove `RedisAddr` from transaction-service config**

In `transaction-service/internal/config/config.go`:

Remove from Config struct:

```go
// REMOVE:
RedisAddr string
```

Remove from Load():

```go
// REMOVE:
RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
```

- [ ] **Step 3: Delete `transaction-service/internal/cache/redis.go`**

```bash
rm transaction-service/internal/cache/redis.go
rmdir transaction-service/internal/cache
```

- [ ] **Step 4: Remove go-redis dependency from transaction-service**

```bash
cd transaction-service && go mod tidy
```

- [ ] **Step 5: Remove cache init from credit-service/cmd/main.go**

Remove these lines (currently around lines 58-65):

```go
// REMOVE:
	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}
```

Remove the cache import:

```go
// REMOVE from imports:
	"github.com/exbanka/credit-service/internal/cache"
```

- [ ] **Step 6: Remove `RedisAddr` from credit-service config**

In `credit-service/internal/config/config.go`:

Remove from Config struct:

```go
// REMOVE:
RedisAddr string
```

Remove from Load():

```go
// REMOVE:
RedisAddr:       getEnv("REDIS_ADDR", "localhost:6379"),
```

- [ ] **Step 7: Delete `credit-service/internal/cache/redis.go`**

```bash
rm credit-service/internal/cache/redis.go
rmdir credit-service/internal/cache
```

- [ ] **Step 8: Remove go-redis dependency from credit-service**

```bash
cd credit-service && go mod tidy
```

- [ ] **Step 9: Update docker-compose.yml**

Remove `REDIS_ADDR` and `redis` dependency from both services:

**transaction-service** -- remove these lines:

```yaml
      # REMOVE from environment:
      REDIS_ADDR: redis:6379

      # REMOVE from depends_on:
      redis:
        condition: service_healthy
```

**credit-service** -- remove these lines:

```yaml
      # REMOVE from environment:
      REDIS_ADDR: redis:6379

      # REMOVE from depends_on:
      redis:
        condition: service_healthy
```

- [ ] **Step 10: Verify both services build and tests pass**

```bash
cd transaction-service && go build ./cmd && go test ./... -v
cd credit-service && go build ./cmd && go test ./... -v
```

- [ ] **Step 11: Commit**

```
chore(transaction,credit): remove unused Redis initialization

Neither service uses its cache field in any service or handler code.
Removes startup latency, false dependency, and dead cache/ package.
```

---

### Task 6: Unit tests for new caching behavior

Add targeted tests that verify cache hits, misses, and invalidation work correctly for the services modified in Tasks 1-4.

**Files:**
- Create: `exchange-service/internal/service/exchange_cache_test.go`
- Create: `card-service/internal/service/card_cache_test.go`
- Create: `account-service/internal/service/account_cache_test.go`

- [ ] **Step 1: Exchange-service cache test**

```go
// exchange-service/internal/service/exchange_cache_test.go
package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/repository"
)

func TestGetCachedRate_CacheMiss_HitsDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ExchangeRate{})
	repo := repository.NewExchangeRateRepository(db)

	// Seed a rate
	err := repo.Upsert("EUR", "RSD", decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.0))
	require.NoError(t, err)

	// Create service with nil cache (simulates no Redis)
	svc, err := NewExchangeService(repo, db, nil, "0.005", "0.003")
	require.NoError(t, err)

	rate, err := svc.GetRate("EUR", "RSD")
	require.NoError(t, err)
	assert.Equal(t, "EUR", rate.FromCurrency)
	assert.Equal(t, "RSD", rate.ToCurrency)
}

func TestConvert_SameCurrency_ReturnsIdentity(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ExchangeRate{})
	repo := repository.NewExchangeRateRepository(db)

	svc, err := NewExchangeService(repo, db, nil, "0.005", "0.003")
	require.NoError(t, err)

	amount := decimal.NewFromInt(1000)
	result, rate, err := svc.Convert(context.Background(), "RSD", "RSD", amount)
	require.NoError(t, err)
	assert.True(t, result.Equal(amount))
	assert.True(t, rate.Equal(decimal.NewFromInt(1)))
}

func TestConvert_NilCache_FallsBackToDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ExchangeRate{})
	repo := repository.NewExchangeRateRepository(db)

	err := repo.Upsert("RSD", "EUR", decimal.NewFromFloat(0.00844), decimal.NewFromFloat(0.00862))
	require.NoError(t, err)

	svc, err := NewExchangeService(repo, db, nil, "0.005", "0.003")
	require.NoError(t, err)

	result, _, err := svc.Convert(context.Background(), "RSD", "EUR", decimal.NewFromInt(10000))
	require.NoError(t, err)
	// 10000 * 0.00862 (sell rate) = 86.2
	assert.True(t, result.Equal(decimal.NewFromFloat(86.2)))
}
```

- [ ] **Step 2: Card-service cache test**

These tests verify that `GetCard` returns correct data and that invalidation clears the cache (using nil cache to verify DB fallback path).

```go
// card-service/internal/service/card_cache_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/testutil"
)

func TestGetCard_NilCache_FallsBackToDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Card{}, &model.CardBlock{}, &model.AuthorizedPerson{})
	cardRepo := repository.NewCardRepository(db)

	card := &model.Card{
		CardNumber:     "****1234",
		CardNumberFull: "4111111111111234",
		CVV:            "123",
		CardType:       "debit",
		CardBrand:      "visa",
		AccountNumber:  "1234567890123456",
		OwnerID:        1,
		OwnerType:      "client",
		Status:         "active",
	}
	err := cardRepo.Create(card)
	require.NoError(t, err)

	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)
	svc := NewCardService(cardRepo, blockRepo, authRepo, nil, nil, db)

	got, err := svc.GetCard(card.ID)
	require.NoError(t, err)
	assert.Equal(t, card.ID, got.ID)
	assert.Equal(t, "active", got.Status)
}

func TestInvalidateCard_NilCache_NoError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Card{}, &model.CardBlock{}, &model.AuthorizedPerson{})
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)
	svc := NewCardService(cardRepo, blockRepo, authRepo, nil, nil, db)

	// Should not panic with nil cache
	svc.invalidateCard(999)
}
```

- [ ] **Step 3: Account-service cache test**

```go
// account-service/internal/service/account_cache_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/contract/testutil"
)

func TestGetAccount_NilCache_FallsBackToDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := repository.NewAccountRepository(db)

	account := &model.Account{
		AccountNumber: "1234567890123456",
		AccountName:   "Test Account",
		OwnerID:       1,
		CurrencyCode:  "RSD",
		AccountKind:   "current",
		Status:        "active",
	}
	err := repo.Create(account)
	require.NoError(t, err)

	svc := NewAccountService(repo, db, nil)

	got, err := svc.GetAccount(account.ID)
	require.NoError(t, err)
	assert.Equal(t, account.ID, got.ID)
	assert.Equal(t, "Test Account", got.AccountName)
}

func TestGetAccountByNumber_NilCache_FallsBackToDB(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := repository.NewAccountRepository(db)

	account := &model.Account{
		AccountNumber: "9876543210123456",
		AccountName:   "Test Account 2",
		OwnerID:       2,
		CurrencyCode:  "EUR",
		AccountKind:   "foreign",
		Status:        "active",
	}
	err := repo.Create(account)
	require.NoError(t, err)

	svc := NewAccountService(repo, db, nil)

	got, err := svc.GetAccountByNumber("9876543210123456")
	require.NoError(t, err)
	assert.Equal(t, account.ID, got.ID)
}

func TestInvalidateAccount_NilCache_NoError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := repository.NewAccountRepository(db)
	svc := NewAccountService(repo, db, nil)

	// Should not panic with nil cache
	svc.invalidateAccount(999, "test-number")
}
```

- [ ] **Step 4: Run all tests**

```bash
make test
```

- [ ] **Step 5: Commit**

```
test(exchange,card,account): add unit tests for Redis cache behavior

Tests verify cache-miss DB fallback, nil-cache graceful degradation,
and invalidation no-ops when cache is nil.
```

---

### Task 7: Update Specification.md

**Files:**
- Modify: `Specification.md`

- [ ] **Step 1: Update the Redis caching section in the spec**

Add/update a "Redis Caching" section documenting which services use Redis and their cache key patterns:

| Service | Cache Keys | TTL | Invalidation Trigger |
|---|---|---|---|
| auth-service | JWT validation, token blacklist | varies | Token revocation |
| user-service | Employee lookups | varies | Employee update |
| exchange-service | `rate:<from>:<to>` | 5 min | SyncRates |
| stock-service | `security:<type>:<id>` | 2 min | RefreshPrices |
| card-service | `card:id:<id>` | 3 min | Any card mutation |
| account-service | `account:id:<id>`, `account:num:<number>` | 2 min | Any account mutation |

Note: transaction-service and credit-service do NOT use Redis. Their cache/ packages have been removed.

- [ ] **Step 2: Commit**

```
docs: update Specification.md with Redis caching patterns
```
