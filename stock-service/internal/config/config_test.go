package config

import (
	"strings"
	"testing"
)

func TestConfig_Load_Defaults(t *testing.T) {
	t.Setenv("STOCK_DB_HOST", "")
	t.Setenv("SECURITY_SYNC_INTERVAL_MINUTES", "")
	cfg := Load()
	if cfg.DBHost != "localhost" {
		t.Errorf("default DBHost = %q", cfg.DBHost)
	}
	if cfg.DBPort != "5440" {
		t.Errorf("default DBPort = %q", cfg.DBPort)
	}
	if cfg.SecuritySyncIntervalMins != 15 {
		t.Errorf("sync interval = %d (default 15)", cfg.SecuritySyncIntervalMins)
	}
	if cfg.OwnBankCode == "" {
		t.Errorf("expected OwnBankCode default")
	}
}

func TestConfig_Load_FromEnv(t *testing.T) {
	t.Setenv("STOCK_DB_HOST", "myhost")
	t.Setenv("STOCK_DB_PORT", "9999")
	t.Setenv("OWN_BANK_CODE", "555")
	t.Setenv("SECURITY_SYNC_INTERVAL_MINUTES", "30")
	t.Setenv("OTC_EXPIRY_BATCH_SIZE", "200")
	cfg := Load()
	if cfg.DBHost != "myhost" {
		t.Errorf("DBHost = %q", cfg.DBHost)
	}
	if cfg.DBPort != "9999" {
		t.Errorf("DBPort = %q", cfg.DBPort)
	}
	if cfg.OwnBankCode != "555" {
		t.Errorf("OwnBankCode = %q", cfg.OwnBankCode)
	}
	if cfg.SecuritySyncIntervalMins != 30 {
		t.Errorf("sync interval = %d", cfg.SecuritySyncIntervalMins)
	}
	if cfg.OTCExpiryBatchSize != 200 {
		t.Errorf("batch size = %d", cfg.OTCExpiryBatchSize)
	}
}

func TestConfig_Load_BadIntFallsBack(t *testing.T) {
	t.Setenv("SECURITY_SYNC_INTERVAL_MINUTES", "not-a-number")
	t.Setenv("OTC_EXPIRY_BATCH_SIZE", "negative") // invalid → fallback
	cfg := Load()
	if cfg.SecuritySyncIntervalMins != 15 {
		t.Errorf("expected fallback 15, got %d", cfg.SecuritySyncIntervalMins)
	}
	if cfg.OTCExpiryBatchSize != 500 {
		t.Errorf("expected fallback 500, got %d", cfg.OTCExpiryBatchSize)
	}
}

func TestConfig_Load_NegativeInt_FallsBack(t *testing.T) {
	t.Setenv("SECURITY_SYNC_INTERVAL_MINUTES", "-5")
	cfg := Load()
	if cfg.SecuritySyncIntervalMins != 15 {
		t.Errorf("expected fallback 15 for negative, got %d", cfg.SecuritySyncIntervalMins)
	}
}

func TestConfig_DSN(t *testing.T) {
	cfg := &Config{
		DBHost: "localhost", DBPort: "5440", DBUser: "u", DBPassword: "p",
		DBName: "stock_db", DBSslmode: "disable",
	}
	dsn := cfg.DSN()
	if !strings.Contains(dsn, "host=localhost") {
		t.Errorf("DSN missing host: %q", dsn)
	}
	if !strings.Contains(dsn, "dbname=stock_db") {
		t.Errorf("DSN missing dbname: %q", dsn)
	}
	if !strings.Contains(dsn, "TimeZone=UTC") {
		t.Errorf("DSN missing tz: %q", dsn)
	}
}

func TestGetEnv_Defaults(t *testing.T) {
	t.Setenv("UNIT_TEST_DOES_NOT_EXIST", "")
	if got := getEnv("UNIT_TEST_DOES_NOT_EXIST", "fallback"); got != "fallback" {
		t.Errorf("got %q want fallback", got)
	}
	t.Setenv("UNIT_TEST_DOES_NOT_EXIST", "real")
	if got := getEnv("UNIT_TEST_DOES_NOT_EXIST", "fallback"); got != "real" {
		t.Errorf("got %q want real", got)
	}
}

func TestGetEnvInt_Defaults(t *testing.T) {
	t.Setenv("UNIT_TEST_INT", "")
	if got := getEnvInt("UNIT_TEST_INT", 99); got != 99 {
		t.Errorf("got %d", got)
	}
	t.Setenv("UNIT_TEST_INT", "42")
	if got := getEnvInt("UNIT_TEST_INT", 99); got != 42 {
		t.Errorf("got %d", got)
	}
	t.Setenv("UNIT_TEST_INT", "abc")
	if got := getEnvInt("UNIT_TEST_INT", 99); got != 99 {
		t.Errorf("got %d (expected fallback for invalid)", got)
	}
}
