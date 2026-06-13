package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"TRANSACTION_DB_HOST", "TRANSACTION_DB_PORT", "TRANSACTION_DB_USER",
		"TRANSACTION_DB_PASSWORD", "TRANSACTION_DB_NAME", "TRANSACTION_GRPC_ADDR",
		"KAFKA_BROKERS", "ACCOUNT_GRPC_ADDR", "EXCHANGE_GRPC_ADDR",
		"VERIFICATION_GRPC_ADDR", "METRICS_PORT", "TRANSACTION_DB_SSLMODE",
	} {
		_ = os.Unsetenv(k)
	}
	cfg := Load()
	if cfg.DBHost != "localhost" || cfg.DBPort != "5437" || cfg.DBName != "transactiondb" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.GRPCAddr != ":50057" || cfg.AccountGRPCAddr != "localhost:50055" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("TRANSACTION_DB_HOST", "h")
	t.Setenv("ACCOUNT_GRPC_ADDR", "acct:1")
	cfg := Load()
	if cfg.DBHost != "h" || cfg.AccountGRPCAddr != "acct:1" {
		t.Fatalf("overrides wrong: %+v", cfg)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("X_TS_K", "v")
	if getEnv("X_TS_K", "fb") != "v" {
		t.Fatal("getEnv override")
	}
	_ = os.Unsetenv("X_TS_K_M")
	if getEnv("X_TS_K_M", "fb") != "fb" {
		t.Fatal("getEnv fallback")
	}
}

func TestDSN(t *testing.T) {
	cfg := &Config{DBHost: "h", DBPort: "1", DBUser: "u", DBPassword: "p", DBName: "d"}
	dsn := cfg.DSN()
	for _, want := range []string{"host=h", "port=1", "user=u", "password=p", "dbname=d", "TimeZone=UTC"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("dsn missing %q: %s", want, dsn)
		}
	}
}
