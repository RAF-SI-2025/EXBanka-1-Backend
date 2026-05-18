package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoad_DefaultsWhenNoEnvSet(t *testing.T) {
	// Clear all relevant vars to exercise the fallback branch in getEnv.
	for _, k := range []string{
		"AUTH_DB_HOST", "AUTH_DB_PORT", "AUTH_DB_USER", "AUTH_DB_PASSWORD", "AUTH_DB_NAME",
		"AUTH_GRPC_ADDR", "USER_GRPC_ADDR", "KAFKA_BROKERS", "JWT_SECRET",
		"JWT_ACCESS_EXPIRY", "JWT_REFRESH_EXPIRY", "REDIS_ADDR", "FRONTEND_BASE_URL",
		"PASSWORD_PEPPER", "MOBILE_REFRESH_EXPIRY", "MOBILE_ACTIVATION_EXPIRY",
		"METRICS_PORT",
	} {
		t.Setenv(k, "") // Setenv with empty unsets effectively because getEnv treats "" as fallback.
	}

	cfg := Load()
	assert.Equal(t, "localhost", cfg.DBHost)
	assert.Equal(t, "5432", cfg.DBPort)
	assert.Equal(t, "postgres", cfg.DBUser)
	assert.Equal(t, "postgres", cfg.DBPassword)
	assert.Equal(t, "authdb", cfg.DBName)
	assert.Equal(t, ":50051", cfg.GRPCAddr)
	assert.Equal(t, "localhost:50052", cfg.UserGRPCAddr)
	assert.Equal(t, "localhost:9092", cfg.KafkaBrokers)
	assert.Equal(t, "change-me", cfg.JWTSecret)
	assert.Equal(t, 15*time.Minute, cfg.AccessExpiry)
	assert.Equal(t, 168*time.Hour, cfg.RefreshExpiry)
	assert.Equal(t, "localhost:6379", cfg.RedisAddr)
	assert.Equal(t, "http://localhost:3000", cfg.FrontendBaseURL)
	assert.Equal(t, "", cfg.PasswordPepper)
	assert.Equal(t, 2160*time.Hour, cfg.MobileRefreshExpiry)
	assert.Equal(t, 15*time.Minute, cfg.MobileActivationExpiry)
	assert.Equal(t, "9101", cfg.MetricsPort)
}

func TestLoad_ReadsEnvOverrides(t *testing.T) {
	t.Setenv("AUTH_DB_HOST", "my-db-host")
	t.Setenv("AUTH_DB_PORT", "5599")
	t.Setenv("JWT_SECRET", "supersecret")
	t.Setenv("JWT_ACCESS_EXPIRY", "30m")
	t.Setenv("MOBILE_ACTIVATION_EXPIRY", "10m")

	cfg := Load()
	assert.Equal(t, "my-db-host", cfg.DBHost)
	assert.Equal(t, "5599", cfg.DBPort)
	assert.Equal(t, "supersecret", cfg.JWTSecret)
	assert.Equal(t, 30*time.Minute, cfg.AccessExpiry)
	assert.Equal(t, 10*time.Minute, cfg.MobileActivationExpiry)
}

func TestGetEnv_FallbackOnEmpty(t *testing.T) {
	t.Setenv("EXBANKA_TEST_VAR", "")
	assert.Equal(t, "fallback", getEnv("EXBANKA_TEST_VAR", "fallback"))

	t.Setenv("EXBANKA_TEST_VAR", "actual-value")
	assert.Equal(t, "actual-value", getEnv("EXBANKA_TEST_VAR", "fallback"))
}

func TestConfig_DSN_BuildsConnectionString(t *testing.T) {
	t.Setenv("AUTH_DB_SSLMODE", "disable")

	cfg := &Config{
		DBHost: "h", DBPort: "5432", DBUser: "u", DBPassword: "p", DBName: "n",
	}
	dsn := cfg.DSN()
	for _, want := range []string{
		"host=h", "port=5432", "user=u", "password=p", "dbname=n",
		"sslmode=disable", "TimeZone=UTC",
	} {
		assert.True(t, strings.Contains(dsn, want), "DSN %q must contain %q", dsn, want)
	}
}

func TestConfig_DSN_DefaultSSLMode(t *testing.T) {
	t.Setenv("AUTH_DB_SSLMODE", "")
	cfg := &Config{DBHost: "h", DBPort: "5432", DBUser: "u", DBPassword: "p", DBName: "n"}
	assert.Contains(t, cfg.DSN(), "sslmode=require")
}
