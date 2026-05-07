package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_DefaultsWhenEnvUnset(t *testing.T) {
	for _, k := range []string{
		"CLIENT_DB_HOST", "CLIENT_DB_PORT", "CLIENT_DB_USER", "CLIENT_DB_PASSWORD",
		"CLIENT_DB_NAME", "CLIENT_GRPC_ADDR", "KAFKA_BROKERS",
		"REDIS_ADDR", "USER_GRPC_ADDR", "METRICS_PORT",
	} {
		t.Setenv(k, "")
	}
	cfg := Load()
	assert.Equal(t, "localhost", cfg.DBHost)
	assert.Equal(t, "5434", cfg.DBPort)
	assert.Equal(t, "postgres", cfg.DBUser)
	assert.Equal(t, "postgres", cfg.DBPassword)
	assert.Equal(t, "clientdb", cfg.DBName)
	assert.Equal(t, ":50054", cfg.GRPCAddr)
	assert.Equal(t, "localhost:9092", cfg.KafkaBrokers)
	assert.Equal(t, "localhost:6379", cfg.RedisAddr)
	assert.Equal(t, "localhost:50052", cfg.UserGRPCAddr)
	assert.Equal(t, "9104", cfg.MetricsPort)
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	t.Setenv("CLIENT_DB_HOST", "h")
	t.Setenv("CLIENT_DB_PORT", "1234")
	t.Setenv("CLIENT_DB_USER", "u")
	t.Setenv("CLIENT_DB_PASSWORD", "p")
	t.Setenv("CLIENT_DB_NAME", "n")
	t.Setenv("CLIENT_GRPC_ADDR", ":1")
	t.Setenv("KAFKA_BROKERS", "k:1")
	t.Setenv("REDIS_ADDR", "r:1")
	t.Setenv("USER_GRPC_ADDR", "us:1")
	t.Setenv("METRICS_PORT", "9999")
	cfg := Load()
	assert.Equal(t, "h", cfg.DBHost)
	assert.Equal(t, "1234", cfg.DBPort)
	assert.Equal(t, "u", cfg.DBUser)
	assert.Equal(t, "p", cfg.DBPassword)
	assert.Equal(t, "n", cfg.DBName)
	assert.Equal(t, ":1", cfg.GRPCAddr)
	assert.Equal(t, "k:1", cfg.KafkaBrokers)
	assert.Equal(t, "r:1", cfg.RedisAddr)
	assert.Equal(t, "us:1", cfg.UserGRPCAddr)
	assert.Equal(t, "9999", cfg.MetricsPort)
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
	t.Setenv("CLIENT_DB_SSLMODE", "require")
	cfg := &Config{DBHost: "h", DBPort: "1", DBUser: "u", DBPassword: "p", DBName: "n"}
	assert.Contains(t, cfg.DSN(), "sslmode=require")
}
