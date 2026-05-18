package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/exchange-service/internal/cache"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/repository"
	"github.com/exbanka/exchange-service/internal/service"
)

// mockProvider implements provider.RateProvider.
type mockProvider struct {
	rates map[string]decimal.Decimal // currencyCode -> mid rate per 1 RSD
	err   error
}

func (m *mockProvider) FetchRatesFromRSD() (map[string]decimal.Decimal, error) {
	return m.rates, m.err
}

func newTestService(t *testing.T) (*service.ExchangeService, *repository.ExchangeRateRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)
	svc, err := service.NewExchangeService(repo, db, "0.005", "0.003", nil)
	require.NoError(t, err)
	return svc, repo
}

// seedRate stores a sell rate pair (both directions) directly via the repo.
func seedRates(t *testing.T, repo *repository.ExchangeRateRepository) {
	t.Helper()
	// EUR/RSD sell: 118.5 (bank sells 118.5 RSD per EUR)
	require.NoError(t, repo.Upsert("EUR", "RSD", decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.5)))
	// RSD/EUR sell: 0.00844 (bank sells 0.00844 EUR per RSD)
	require.NoError(t, repo.Upsert("RSD", "EUR", decimal.NewFromFloat(0.00833), decimal.NewFromFloat(0.00844)))
	// USD/RSD sell: 108.0
	require.NoError(t, repo.Upsert("USD", "RSD", decimal.NewFromFloat(106.0), decimal.NewFromFloat(108.0)))
	// RSD/USD sell: 0.00926
	require.NoError(t, repo.Upsert("RSD", "USD", decimal.NewFromFloat(0.00916), decimal.NewFromFloat(0.00926)))
}

// ── ListRates / GetRate ─────────────────────────────────────────────────────

func TestListRates_ReturnsAllStored(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	rates, err := svc.ListRates()
	require.NoError(t, err)
	if len(rates) < 4 {
		t.Errorf("expected at least 4 rates, got %d", len(rates))
	}
}

func TestListRates_EmptyRepo(t *testing.T) {
	svc, _ := newTestService(t)
	rates, err := svc.ListRates()
	require.NoError(t, err)
	if len(rates) != 0 {
		t.Errorf("expected empty list, got %d", len(rates))
	}
}

func TestGetRate_Success(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	rate, err := svc.GetRate("EUR", "RSD")
	require.NoError(t, err)
	if !rate.SellRate.Equal(decimal.NewFromFloat(118.5)) {
		t.Errorf("unexpected sell rate: %s", rate.SellRate.String())
	}
}

func TestGetRate_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.GetRate("XYZ", "RSD")
	if err == nil {
		t.Fatal("expected error for unknown currency, got nil")
	}
}

// ── Convert (no commission) ───────────────────────────────────────────────────

func TestConvert_SameCurrency(t *testing.T) {
	svc, _ := newTestService(t)
	got, rate, err := svc.Convert(context.Background(), "EUR", "EUR", decimal.NewFromFloat(100))
	require.NoError(t, err)
	assert.True(t, got.Equal(decimal.NewFromFloat(100)))
	assert.True(t, rate.Equal(decimal.NewFromInt(1)))
}

func TestConvert_RSDToForeign(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	// 1000 RSD → EUR at sellRate 0.00844
	got, _, err := svc.Convert(context.Background(), "RSD", "EUR", decimal.NewFromFloat(1000))
	require.NoError(t, err)
	expected := decimal.NewFromFloat(1000 * 0.00844)
	assert.True(t, got.Sub(expected).Abs().LessThan(decimal.NewFromFloat(0.01)))
}

func TestConvert_ForeignToRSD(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	// 100 EUR → RSD at sellRate 118.5
	got, _, err := svc.Convert(context.Background(), "EUR", "RSD", decimal.NewFromFloat(100))
	require.NoError(t, err)
	expected := decimal.NewFromFloat(100 * 118.5)
	assert.True(t, got.Sub(expected).Abs().LessThan(decimal.NewFromFloat(0.01)))
}

func TestConvert_CrossCurrencyViaTwoLegs(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	// 100 EUR → USD via RSD
	// Step 1: 100 EUR * 118.5 = 11850 RSD
	// Step 2: 11850 RSD * 0.00926 = 109.73 USD
	got, _, err := svc.Convert(context.Background(), "EUR", "USD", decimal.NewFromFloat(100))
	require.NoError(t, err)
	expected := decimal.NewFromFloat(100 * 118.5 * 0.00926)
	assert.True(t, got.Sub(expected).Abs().LessThan(decimal.NewFromFloat(0.01)))
}

// ── Calculate (with commission) ───────────────────────────────────────────────

func TestCalculate_RSDToForeign_AppliesCommission(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	// 1000 RSD → EUR, commissionRate = 0.005
	// gross = 1000 * 0.00844 = 8.44
	// commission = 8.44 * 0.005 = 0.0422
	// net = 8.44 - 0.0422 = 8.3978
	net, commRate, effRate, err := svc.Calculate(context.Background(), "RSD", "EUR", decimal.NewFromFloat(1000))
	require.NoError(t, err)
	f, _ := net.Float64()
	assert.InDelta(t, 8.3978, f, 0.01)
	assert.Equal(t, "0.005", commRate.String())
	_ = effRate // just assert no error, exact value covered by Convert tests
}

func TestCalculate_CrossCurrency_AppliesCommissionPerLeg(t *testing.T) {
	svc, repo := newTestService(t)
	seedRates(t, repo)
	// 100 EUR → USD, commissionRate = 0.005 per leg
	// Step 1: 100 EUR * 118.5 = 11850 RSD; commission = 11850 * 0.005 = 59.25; net = 11790.75
	// Step 2: 11790.75 * 0.00926 = 109.18 USD; commission = 109.18 * 0.005 = 0.5459; net = 108.63 USD
	net, _, _, err := svc.Calculate(context.Background(), "EUR", "USD", decimal.NewFromFloat(100))
	require.NoError(t, err)
	f, _ := net.Float64()
	assert.InDelta(t, 108.63, f, 0.1)
}

// ── SyncRates ─────────────────────────────────────────────────────────────────

func TestSyncRates_PersistsForwardAndInverse(t *testing.T) {
	svc, repo := newTestService(t)
	p := &mockProvider{rates: map[string]decimal.Decimal{
		"EUR": decimal.NewFromFloat(0.00851), // 1 RSD = 0.00851 EUR → 1 EUR ≈ 117.5 RSD
	}}
	err := svc.SyncRates(context.Background(), p)
	require.NoError(t, err)

	// RSD → EUR stored
	rsdEur, err := repo.GetByPair("RSD", "EUR")
	require.NoError(t, err)
	f, _ := rsdEur.SellRate.Float64()
	// sellRate = 0.00851 * (1 + 0.003) = 0.008536
	assert.InDelta(t, 0.008536, f, 0.0001)

	// EUR → RSD stored (inverse)
	eurRsd, err := repo.GetByPair("EUR", "RSD")
	require.NoError(t, err)
	f2, _ := eurRsd.SellRate.Float64()
	// inverse mid = 1/0.00851 = 117.5; sellRate = 117.5 * (1 + 0.003) = 117.85
	assert.InDelta(t, 117.85, f2, 0.2)
}

func TestSyncRates_ProviderError_DoesNotWipeExistingRates(t *testing.T) {
	svc, repo := newTestService(t)
	// Pre-seed a rate
	require.NoError(t, repo.Upsert("EUR", "RSD", decimal.NewFromFloat(116), decimal.NewFromFloat(118)))

	p := &mockProvider{err: errors.New("API down")}
	err := svc.SyncRates(context.Background(), p)
	assert.Error(t, err, "SyncRates must propagate the error")

	// Existing rate must still be there
	rate, err2 := repo.GetByPair("EUR", "RSD")
	require.NoError(t, err2)
	assert.True(t, rate.SellRate.Equal(decimal.NewFromFloat(118)))
}

// failingUpserterAfterN satisfies service.RateUpserter and wraps a real repository,
// returning an error on the Nth call to UpsertInTx. Used to inject mid-sync failure.
type failingUpserterAfterN struct {
	inner  *repository.ExchangeRateRepository
	failAt int
	callN  int
}

// Compile-time check: *failingUpserterAfterN must satisfy service.RateUpserter.
var _ service.RateUpserter = (*failingUpserterAfterN)(nil)

func (f *failingUpserterAfterN) UpsertInTx(tx *gorm.DB, from, to string, buy, sell decimal.Decimal) error {
	f.callN++
	if f.callN >= f.failAt {
		return errors.New("injected upsert failure")
	}
	return f.inner.UpsertInTx(tx, from, to, buy, sell)
}

// TestNewExchangeService_InvalidCommission ensures the constructor surfaces
// a parse error when the commission rate is not a valid decimal.
func TestNewExchangeService_InvalidCommission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)

	svc, err := service.NewExchangeService(repo, db, "not-a-number", "0.003", nil)
	require.Error(t, err)
	assert.Nil(t, svc)
}

// TestNewExchangeService_InvalidSpread ensures the constructor surfaces a
// parse error when the spread is not a valid decimal.
func TestNewExchangeService_InvalidSpread(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)

	svc, err := service.NewExchangeService(repo, db, "0.005", "bad", nil)
	require.Error(t, err)
	assert.Nil(t, svc)
}

// TestNewExchangeServiceWithUpserter_InvalidCommission propagates the underlying
// constructor error.
func TestNewExchangeServiceWithUpserter_InvalidCommission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)

	svc, err := service.NewExchangeServiceWithUpserter(repo, repo, db, "junk", "0.003")
	require.Error(t, err)
	assert.Nil(t, svc)
}

// TestSyncRates_SkipsZeroMidRates verifies pairs with mid==0 are skipped.
func TestSyncRates_SkipsZeroMidRates(t *testing.T) {
	svc, repo := newTestService(t)
	p := &mockProvider{rates: map[string]decimal.Decimal{
		"EUR": decimal.Zero,
		"USD": decimal.NewFromFloat(0.00926),
	}}
	require.NoError(t, svc.SyncRates(context.Background(), p))

	// EUR pair should not have been written (skipped).
	_, err := repo.GetByPair("RSD", "EUR")
	require.Error(t, err, "zero mid rate should be skipped — EUR/RSD must not exist")

	// USD pair should be present.
	_, err = repo.GetByPair("RSD", "USD")
	require.NoError(t, err)
}

// TestCalculate_SameCurrency returns input amount with zero commission and rate=1.
func TestCalculate_SameCurrency(t *testing.T) {
	svc, _ := newTestService(t)
	net, comm, eff, err := svc.Calculate(context.Background(), "EUR", "EUR", decimal.NewFromFloat(50))
	require.NoError(t, err)
	assert.True(t, net.Equal(decimal.NewFromFloat(50)))
	assert.True(t, comm.Equal(decimal.Zero))
	assert.True(t, eff.Equal(decimal.NewFromInt(1)))
}

// TestCalculate_RSDToForeign_LookupFailure surfaces the underlying error.
func TestCalculate_RSDToForeign_LookupFailure(t *testing.T) {
	svc, _ := newTestService(t)
	_, _, _, err := svc.Calculate(context.Background(), "RSD", "EUR", decimal.NewFromFloat(100))
	require.Error(t, err)
}

// TestCalculate_CrossCurrency_FirstLegMissing returns an error when the
// from-leg lookup fails.
func TestCalculate_CrossCurrency_FirstLegMissing(t *testing.T) {
	svc, repo := newTestService(t)
	// Seed only the second leg (RSD → USD).
	require.NoError(t, repo.Upsert("RSD", "USD", decimal.NewFromFloat(0.00916), decimal.NewFromFloat(0.00926)))
	_, _, _, err := svc.Calculate(context.Background(), "EUR", "USD", decimal.NewFromFloat(100))
	require.Error(t, err)
}

// TestCalculate_CrossCurrency_SecondLegMissing returns an error when the
// to-leg lookup fails.
func TestCalculate_CrossCurrency_SecondLegMissing(t *testing.T) {
	svc, repo := newTestService(t)
	// Seed only the first leg (EUR → RSD).
	require.NoError(t, repo.Upsert("EUR", "RSD", decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.5)))
	_, _, _, err := svc.Calculate(context.Background(), "EUR", "USD", decimal.NewFromFloat(100))
	require.Error(t, err)
}

// TestConvert_RSDToForeign_NotFound surfaces ErrRateNotFound when the pair is missing.
func TestConvert_RSDToForeign_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, _, err := svc.Convert(context.Background(), "RSD", "EUR", decimal.NewFromFloat(100))
	require.Error(t, err)
}

// TestConvert_TwoLeg_FirstLegMissing returns an error when the from-leg lookup fails.
func TestConvert_TwoLeg_FirstLegMissing(t *testing.T) {
	svc, repo := newTestService(t)
	// Only seed the second leg.
	require.NoError(t, repo.Upsert("RSD", "USD", decimal.NewFromFloat(0.00916), decimal.NewFromFloat(0.00926)))
	_, _, err := svc.Convert(context.Background(), "EUR", "USD", decimal.NewFromFloat(100))
	require.Error(t, err)
}

// TestConvert_TwoLeg_SecondLegMissing returns an error when the to-leg lookup fails.
func TestConvert_TwoLeg_SecondLegMissing(t *testing.T) {
	svc, repo := newTestService(t)
	// Only seed the first leg.
	require.NoError(t, repo.Upsert("EUR", "RSD", decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.5)))
	_, _, err := svc.Convert(context.Background(), "EUR", "USD", decimal.NewFromFloat(100))
	require.Error(t, err)
}

// TestSyncRates_WithCache_InvalidatesAfterSuccess wires a real Redis cache via
// miniredis and asserts that a successful sync wipes all "rate:*" keys.
func TestSyncRates_WithCache_InvalidatesAfterSuccess(t *testing.T) {
	mr := miniredis.RunT(t)
	redisCache, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	defer redisCache.Close()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)
	svc, err := service.NewExchangeService(repo, db, "0.005", "0.003", redisCache)
	require.NoError(t, err)

	// Pre-populate cache with a stale rate entry.
	require.NoError(t, redisCache.Set(context.Background(), "rate:EUR:RSD", &model.ExchangeRate{
		FromCurrency: "EUR", ToCurrency: "RSD",
		BuyRate: decimal.NewFromFloat(1), SellRate: decimal.NewFromFloat(2),
	}, 5*time.Minute))

	p := &mockProvider{rates: map[string]decimal.Decimal{
		"EUR": decimal.NewFromFloat(0.00851),
	}}
	require.NoError(t, svc.SyncRates(context.Background(), p))

	// Cache must have been invalidated.
	var dest model.ExchangeRate
	err = redisCache.Get(context.Background(), "rate:EUR:RSD", &dest)
	assert.Error(t, err, "rate:* keys must be cleared after successful sync")
}

// TestGetRate_CacheHit verifies that on a cache hit, the repo is not touched.
// We seed the cache with a value the repo does not contain, then GetRate must
// still succeed by returning the cached value.
func TestGetRate_CacheHit(t *testing.T) {
	mr := miniredis.RunT(t)
	redisCache, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	defer redisCache.Close()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)
	svc, err := service.NewExchangeService(repo, db, "0.005", "0.003", redisCache)
	require.NoError(t, err)

	// Seed the cache with a rate that does NOT exist in the DB.
	cached := &model.ExchangeRate{
		FromCurrency: "EUR", ToCurrency: "RSD",
		BuyRate: decimal.NewFromFloat(116.0), SellRate: decimal.NewFromFloat(120.0),
	}
	require.NoError(t, redisCache.Set(context.Background(), "rate:EUR:RSD", cached, 5*time.Minute))

	rate, err := svc.GetRate("EUR", "RSD")
	require.NoError(t, err)
	assert.True(t, rate.SellRate.Equal(decimal.NewFromFloat(120.0)),
		"value must come from cache, not DB")
}

// TestGetRate_CacheMissPopulatesCache verifies that on a cache miss, the repo
// fallback succeeds and the value is then written into the cache.
func TestGetRate_CacheMissPopulatesCache(t *testing.T) {
	mr := miniredis.RunT(t)
	redisCache, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	defer redisCache.Close()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)
	svc, err := service.NewExchangeService(repo, db, "0.005", "0.003", redisCache)
	require.NoError(t, err)

	// Seed only the DB (cache is empty).
	require.NoError(t, repo.Upsert("EUR", "RSD",
		decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.5)))

	rate, err := svc.GetRate("EUR", "RSD")
	require.NoError(t, err)
	assert.True(t, rate.SellRate.Equal(decimal.NewFromFloat(118.5)))

	// Cache must now hold the value.
	var fromCache model.ExchangeRate
	require.NoError(t, redisCache.Get(context.Background(), "rate:EUR:RSD", &fromCache))
	assert.True(t, fromCache.SellRate.Equal(decimal.NewFromFloat(118.5)))
}

// TestSyncRates_AtomicOnFailure verifies that if any single upsert fails during sync,
// NO rate changes are committed (all-or-nothing). The pre-seeded EUR/RSD value must
// be unchanged even though the failing upsert happens on the 2nd pair.
func TestSyncRates_AtomicOnFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ExchangeRate{}))
	repo := repository.NewExchangeRateRepository(db)

	// Pre-seed EUR/RSD with a known sell rate so we can detect if it changed.
	require.NoError(t, repo.Upsert("EUR", "RSD",
		decimal.NewFromFloat(116.0), decimal.NewFromFloat(118.5)))

	// failing upserter: first call succeeds, second call (RSD/EUR inverse) fails.
	failing := &failingUpserterAfterN{inner: repo, failAt: 2}
	svc, err := service.NewExchangeServiceWithUpserter(repo, failing, db, "0.005", "0.003")
	require.NoError(t, err)

	provider := &mockProvider{rates: map[string]decimal.Decimal{
		"EUR": decimal.NewFromFloat(0.0090), // new mid rate — would change sell from 118.5
	}}
	err = svc.SyncRates(context.Background(), provider)
	require.Error(t, err, "SyncRates must return error when an upsert fails")

	// EUR/RSD sell rate must NOT have changed — the transaction was rolled back.
	rate, fetchErr := repo.GetByPair("EUR", "RSD")
	require.NoError(t, fetchErr)
	assert.True(t, rate.SellRate.Equal(decimal.NewFromFloat(118.5)),
		"EUR/RSD sell rate must be unchanged — transaction must have rolled back on failure")
}
