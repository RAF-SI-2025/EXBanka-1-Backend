package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"GATEWAY_HTTP_ADDR", "AUTH_GRPC_ADDR", "USER_GRPC_ADDR", "CLIENT_GRPC_ADDR",
		"ACCOUNT_GRPC_ADDR", "CARD_GRPC_ADDR", "TRANSACTION_GRPC_ADDR", "CREDIT_GRPC_ADDR",
		"EXCHANGE_GRPC_ADDR", "STOCK_GRPC_ADDR", "VERIFICATION_GRPC_ADDR",
		"NOTIFICATION_GRPC_ADDR", "KAFKA_BROKERS", "METRICS_PORT", "REDIS_ADDR",
		"OWN_BANK_CODE",
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	cfg := Load()
	if cfg.HTTPAddr != ":8080" || cfg.AuthGRPCAddr != "localhost:50051" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.OwnBankCode != "111" || cfg.RedisAddr != "localhost:6379" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("GATEWAY_HTTP_ADDR", ":9999")
	t.Setenv("OWN_BANK_CODE", "777")
	t.Setenv("KAFKA_BROKERS", "k:1")
	cfg := Load()
	if cfg.HTTPAddr != ":9999" || cfg.OwnBankCode != "777" || cfg.KafkaBrokers != "k:1" {
		t.Fatalf("overrides wrong: %+v", cfg)
	}
}

func TestLoad_RateLimitDefaults(t *testing.T) {
	for _, k := range []string{"RATE_LIMIT_GLOBAL_PER_MIN", "RATE_LIMIT_LOGIN_PER_5MIN", "RATE_LIMIT_RESET_PER_5MIN"} {
		_ = os.Unsetenv(k)
	}
	cfg := Load()
	if cfg.RateLimitGlobalPerMin != 3000 {
		t.Fatalf("global default: want 3000, got %d", cfg.RateLimitGlobalPerMin)
	}
	if cfg.RateLimitLoginPer5Min != 20 {
		t.Fatalf("login default: want 20, got %d", cfg.RateLimitLoginPer5Min)
	}
	if cfg.RateLimitResetPer5Min != 5 {
		t.Fatalf("reset default: want 5, got %d", cfg.RateLimitResetPer5Min)
	}
}

func TestLoad_RateLimitOverride(t *testing.T) {
	t.Setenv("RATE_LIMIT_GLOBAL_PER_MIN", "100")
	if got := Load().RateLimitGlobalPerMin; got != 100 {
		t.Fatalf("override: want 100, got %d", got)
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("X_INT_KEY", "42")
	if got := getEnvInt("X_INT_KEY", 7); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
	t.Setenv("X_INT_BAD", "notanint")
	if got := getEnvInt("X_INT_BAD", 7); got != 7 {
		t.Fatalf("bad value should fall back to 7, got %d", got)
	}
	_ = os.Unsetenv("X_INT_MISSING")
	if got := getEnvInt("X_INT_MISSING", 9); got != 9 {
		t.Fatalf("want 9, got %d", got)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("X_TEST_KEY", "set")
	if got := getEnv("X_TEST_KEY", "fb"); got != "set" {
		t.Fatalf("want set, got %q", got)
	}
	_ = os.Unsetenv("X_TEST_KEY_MISSING")
	if got := getEnv("X_TEST_KEY_MISSING", "fb"); got != "fb" {
		t.Fatalf("want fb, got %q", got)
	}
}
