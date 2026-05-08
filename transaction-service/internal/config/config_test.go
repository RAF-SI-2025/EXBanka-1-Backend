package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"TRANSACTION_DB_HOST", "TRANSACTION_DB_PORT", "TRANSACTION_DB_USER",
		"TRANSACTION_DB_PASSWORD", "TRANSACTION_DB_NAME", "TRANSACTION_GRPC_ADDR",
		"KAFKA_BROKERS", "ACCOUNT_GRPC_ADDR", "EXCHANGE_GRPC_ADDR",
		"VERIFICATION_GRPC_ADDR", "STOCK_GRPC_ADDR", "METRICS_PORT",
		"INTERBANK_PREPARE_TIMEOUT", "INTERBANK_COMMIT_TIMEOUT",
		"INTERBANK_RECEIVER_WAIT", "INTERBANK_RECONCILE_INTERVAL",
		"INTERBANK_RECONCILE_MAX_RETRIES", "INTERBANK_RECONCILE_STALE_AFTER",
		"OWN_BANK_CODE",
		"PEER_222_BASE_URL", "PEER_222_INBOUND_KEY", "PEER_222_OUTBOUND_KEY",
		"PEER_333_BASE_URL", "PEER_333_INBOUND_KEY", "PEER_333_OUTBOUND_KEY",
		"PEER_444_BASE_URL", "PEER_444_INBOUND_KEY", "PEER_444_OUTBOUND_KEY",
		"TRANSACTION_DB_SSLMODE",
	} {
		_ = os.Unsetenv(k)
	}
	cfg := Load()
	if cfg.DBHost != "localhost" || cfg.DBPort != "5437" || cfg.DBName != "transactiondb" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.OwnBankCode != "111" || cfg.GRPCAddr != ":50057" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.InterbankPrepareTimeout != 30*time.Second {
		t.Fatalf("prep timeout wrong: %v", cfg.InterbankPrepareTimeout)
	}
	if cfg.InterbankReconcileMaxRetries != 10 {
		t.Fatalf("max retries wrong: %d", cfg.InterbankReconcileMaxRetries)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("TRANSACTION_DB_HOST", "h")
	t.Setenv("OWN_BANK_CODE", "777")
	t.Setenv("INTERBANK_PREPARE_TIMEOUT", "5s")
	t.Setenv("INTERBANK_RECONCILE_MAX_RETRIES", "42")
	t.Setenv("INTERBANK_PREPARE_TIMEOUT", "5s")
	t.Setenv("PEER_222_BASE_URL", "http://p2")
	cfg := Load()
	if cfg.DBHost != "h" || cfg.OwnBankCode != "777" {
		t.Fatalf("overrides wrong: %+v", cfg)
	}
	if cfg.InterbankPrepareTimeout != 5*time.Second {
		t.Fatalf("prep override: %v", cfg.InterbankPrepareTimeout)
	}
	if cfg.InterbankReconcileMaxRetries != 42 {
		t.Fatalf("max retries override: %d", cfg.InterbankReconcileMaxRetries)
	}
	if cfg.Peer222BaseURL != "http://p2" {
		t.Fatalf("peer url override: %q", cfg.Peer222BaseURL)
	}
}

func TestGetEnv_GetDuration_GetInt_BadValues(t *testing.T) {
	t.Setenv("X_TS_K", "v")
	if getEnv("X_TS_K", "fb") != "v" {
		t.Fatal("getEnv override")
	}
	_ = os.Unsetenv("X_TS_K_M")
	if getEnv("X_TS_K_M", "fb") != "fb" {
		t.Fatal("getEnv fallback")
	}
	t.Setenv("X_TS_DUR", "garbage")
	if getDuration("X_TS_DUR", time.Second) != time.Second {
		t.Fatal("getDuration should fall back on bad input")
	}
	t.Setenv("X_TS_DUR", "2s")
	if getDuration("X_TS_DUR", time.Second) != 2*time.Second {
		t.Fatal("getDuration parse")
	}
	_ = os.Unsetenv("X_TS_DUR_M")
	if getDuration("X_TS_DUR_M", time.Hour) != time.Hour {
		t.Fatal("getDuration unset")
	}
	t.Setenv("X_TS_INT", "garbage")
	if getInt("X_TS_INT", 7) != 7 {
		t.Fatal("getInt should fall back on bad input")
	}
	t.Setenv("X_TS_INT", "11")
	if getInt("X_TS_INT", 7) != 11 {
		t.Fatal("getInt parse")
	}
	_ = os.Unsetenv("X_TS_INT_M")
	if getInt("X_TS_INT_M", 7) != 7 {
		t.Fatal("getInt unset")
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
