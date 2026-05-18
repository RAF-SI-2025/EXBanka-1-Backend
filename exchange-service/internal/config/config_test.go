package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_DefaultsWhenEnvUnset(t *testing.T) {
	for _, k := range []string{
		"EXCHANGE_DB_HOST", "EXCHANGE_DB_PORT", "EXCHANGE_DB_USER", "EXCHANGE_DB_PASSWORD",
		"EXCHANGE_DB_NAME", "EXCHANGE_GRPC_ADDR", "KAFKA_BROKERS", "EXCHANGE_API_KEY",
		"EXCHANGE_COMMISSION_RATE", "EXCHANGE_SPREAD", "EXCHANGE_SYNC_INTERVAL_HOURS",
		"REDIS_ADDR", "METRICS_PORT",
	} {
		t.Setenv(k, "")
	}
	cfg := Load()
	assert.Equal(t, "localhost", cfg.DBHost)
	assert.Equal(t, "5439", cfg.DBPort)
	assert.Equal(t, "postgres", cfg.DBUser)
	assert.Equal(t, "postgres", cfg.DBPassword)
	assert.Equal(t, "exchangedb", cfg.DBName)
	assert.Equal(t, ":50059", cfg.GRPCAddr)
	assert.Equal(t, "localhost:9092", cfg.KafkaBrokers)
	assert.Equal(t, "", cfg.APIKey)
	assert.Equal(t, "0.005", cfg.CommissionRate)
	assert.Equal(t, "0.003", cfg.Spread)
	assert.Equal(t, 6, cfg.SyncIntervalHours)
	assert.Equal(t, "localhost:6379", cfg.RedisAddr)
	assert.Equal(t, "9109", cfg.MetricsPort)
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	t.Setenv("EXCHANGE_DB_HOST", "h")
	t.Setenv("EXCHANGE_DB_PORT", "1234")
	t.Setenv("EXCHANGE_DB_USER", "u")
	t.Setenv("EXCHANGE_DB_PASSWORD", "p")
	t.Setenv("EXCHANGE_DB_NAME", "n")
	t.Setenv("EXCHANGE_GRPC_ADDR", ":1")
	t.Setenv("KAFKA_BROKERS", "k:1")
	t.Setenv("EXCHANGE_API_KEY", "topkey")
	t.Setenv("EXCHANGE_COMMISSION_RATE", "0.01")
	t.Setenv("EXCHANGE_SPREAD", "0.02")
	t.Setenv("EXCHANGE_SYNC_INTERVAL_HOURS", "12")
	t.Setenv("REDIS_ADDR", "r:1")
	t.Setenv("METRICS_PORT", "9999")
	cfg := Load()
	assert.Equal(t, "h", cfg.DBHost)
	assert.Equal(t, "1234", cfg.DBPort)
	assert.Equal(t, "u", cfg.DBUser)
	assert.Equal(t, "p", cfg.DBPassword)
	assert.Equal(t, "n", cfg.DBName)
	assert.Equal(t, ":1", cfg.GRPCAddr)
	assert.Equal(t, "k:1", cfg.KafkaBrokers)
	assert.Equal(t, "topkey", cfg.APIKey)
	assert.Equal(t, "0.01", cfg.CommissionRate)
	assert.Equal(t, "0.02", cfg.Spread)
	assert.Equal(t, 12, cfg.SyncIntervalHours)
	assert.Equal(t, "r:1", cfg.RedisAddr)
	assert.Equal(t, "9999", cfg.MetricsPort)
}

func TestLoad_SyncIntervalHours_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("EXCHANGE_SYNC_INTERVAL_HOURS", "not-a-number")
	cfg := Load()
	assert.Equal(t, 6, cfg.SyncIntervalHours)
}

func TestLoad_SyncIntervalHours_NegativeFallsBackToDefault(t *testing.T) {
	t.Setenv("EXCHANGE_SYNC_INTERVAL_HOURS", "-3")
	cfg := Load()
	assert.Equal(t, 6, cfg.SyncIntervalHours)
}

func TestDSN_IncludesAllFields(t *testing.T) {
	cfg := &Config{DBHost: "h", DBPort: "1234", DBUser: "u", DBPassword: "p", DBName: "n"}
	dsn := cfg.DSN()
	for _, want := range []string{
		"host=h", "port=1234", "user=u", "password=p", "dbname=n",
		"sslmode=disable", "TimeZone=UTC",
	} {
		assert.True(t, strings.Contains(dsn, want), "DSN should contain %q, got %q", want, dsn)
	}
}

func TestDSN_HonoursSSLModeOverride(t *testing.T) {
	t.Setenv("EXCHANGE_DB_SSLMODE", "require")
	cfg := &Config{DBHost: "h", DBPort: "1", DBUser: "u", DBPassword: "p", DBName: "n"}
	assert.Contains(t, cfg.DSN(), "sslmode=require")
}
