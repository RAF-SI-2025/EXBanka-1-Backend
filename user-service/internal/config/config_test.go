package config

import (
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Save and clear env vars to ensure defaults are returned.
	keys := []string{
		"USER_DB_HOST", "USER_DB_PORT", "USER_DB_USER", "USER_DB_PASSWORD",
		"USER_DB_NAME", "USER_GRPC_ADDR", "KAFKA_BROKERS", "REDIS_ADDR",
		"METRICS_PORT", "CLIENT_GRPC_ADDR",
	}
	prev := make(map[string]string, len(keys))
	for _, k := range keys {
		prev[k] = ""
		t.Setenv(k, "")
	}
	t.Cleanup(func() {
		for k, v := range prev {
			t.Setenv(k, v)
		}
	})

	cfg := Load()
	if cfg.DBHost != "localhost" {
		t.Errorf("DBHost default: got %q, want localhost", cfg.DBHost)
	}
	if cfg.DBPort != "5432" {
		t.Errorf("DBPort default: got %q, want 5432", cfg.DBPort)
	}
	if cfg.GRPCAddr != ":50052" {
		t.Errorf("GRPCAddr default: got %q, want :50052", cfg.GRPCAddr)
	}
	if cfg.RedisAddr != "localhost:6379" {
		t.Errorf("RedisAddr default: got %q", cfg.RedisAddr)
	}
	if cfg.ClientGRPCAddr != "localhost:50054" {
		t.Errorf("ClientGRPCAddr default: got %q", cfg.ClientGRPCAddr)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("USER_DB_HOST", "db.example.com")
	t.Setenv("USER_DB_PORT", "5454")
	t.Setenv("USER_GRPC_ADDR", ":60052")
	t.Setenv("KAFKA_BROKERS", "kafka1:9092,kafka2:9092")
	t.Setenv("REDIS_ADDR", "redis:6379")

	cfg := Load()
	if cfg.DBHost != "db.example.com" {
		t.Errorf("DBHost: got %q", cfg.DBHost)
	}
	if cfg.DBPort != "5454" {
		t.Errorf("DBPort: got %q", cfg.DBPort)
	}
	if cfg.GRPCAddr != ":60052" {
		t.Errorf("GRPCAddr: got %q", cfg.GRPCAddr)
	}
	if cfg.KafkaBrokers != "kafka1:9092,kafka2:9092" {
		t.Errorf("KafkaBrokers: got %q", cfg.KafkaBrokers)
	}
	if cfg.RedisAddr != "redis:6379" {
		t.Errorf("RedisAddr: got %q", cfg.RedisAddr)
	}
}

func TestDSN_ContainsAllParts(t *testing.T) {
	cfg := &Config{
		DBHost:     "h",
		DBPort:     "1234",
		DBUser:     "u",
		DBPassword: "p",
		DBName:     "d",
	}
	t.Setenv("USER_DB_SSLMODE", "disable")
	dsn := cfg.DSN()
	for _, want := range []string{
		"host=h", "port=1234", "user=u", "password=p", "dbname=d", "sslmode=disable", "TimeZone=UTC",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN missing %q: %s", want, dsn)
		}
	}
}

func TestDSN_DefaultSSLMode(t *testing.T) {
	t.Setenv("USER_DB_SSLMODE", "")
	cfg := &Config{DBHost: "h"}
	dsn := cfg.DSN()
	if !strings.Contains(dsn, "sslmode=require") {
		t.Errorf("default sslmode should be require, got dsn: %s", dsn)
	}
}

func TestGetEnv_FallbackUsedWhenUnset(t *testing.T) {
	t.Setenv("FAKE_KEY_THAT_DOES_NOT_EXIST_xyzzy", "")
	if v := getEnv("FAKE_KEY_THAT_DOES_NOT_EXIST_xyzzy", "fallback"); v != "fallback" {
		t.Errorf("expected fallback, got %q", v)
	}
}

func TestGetEnv_ReturnsValueWhenSet(t *testing.T) {
	t.Setenv("EXBK_TEST_KEY", "set-value")
	if v := getEnv("EXBK_TEST_KEY", "fallback"); v != "set-value" {
		t.Errorf("expected 'set-value', got %q", v)
	}
}
