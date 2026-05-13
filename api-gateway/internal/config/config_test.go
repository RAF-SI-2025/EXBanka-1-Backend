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
