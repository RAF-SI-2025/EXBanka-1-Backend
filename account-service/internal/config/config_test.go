package config

import (
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Best-effort: clear any potentially conflicting env vars in this test only.
	// (t.Setenv would set values; we want absence.)
	for _, k := range []string{
		"ACCOUNT_DB_HOST", "ACCOUNT_DB_PORT", "ACCOUNT_DB_USER", "ACCOUNT_DB_PASSWORD",
		"ACCOUNT_DB_NAME", "ACCOUNT_GRPC_ADDR", "KAFKA_BROKERS", "REDIS_ADDR",
		"CLIENT_GRPC_ADDR", "METRICS_PORT", "OWN_BANK_CODE", "ACCOUNT_DB_SSLMODE",
	} {
		t.Setenv(k, "")
	}
	cfg := Load()
	if cfg.DBHost != "localhost" {
		t.Errorf("default DBHost: got %s want localhost", cfg.DBHost)
	}
	if cfg.DBPort != "5435" {
		t.Errorf("default DBPort: got %s want 5435", cfg.DBPort)
	}
	if cfg.GRPCAddr != ":50055" {
		t.Errorf("default GRPCAddr: got %s want :50055", cfg.GRPCAddr)
	}
	if cfg.OwnBankCode != "111" {
		t.Errorf("default OwnBankCode: got %s want 111", cfg.OwnBankCode)
	}
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	t.Setenv("ACCOUNT_DB_HOST", "db.example.com")
	t.Setenv("ACCOUNT_DB_PORT", "9999")
	t.Setenv("OWN_BANK_CODE", "222")
	t.Setenv("KAFKA_BROKERS", "kafka:9092")

	cfg := Load()
	if cfg.DBHost != "db.example.com" {
		t.Errorf("override DBHost: got %s", cfg.DBHost)
	}
	if cfg.DBPort != "9999" {
		t.Errorf("override DBPort: got %s", cfg.DBPort)
	}
	if cfg.OwnBankCode != "222" {
		t.Errorf("override OwnBankCode: got %s", cfg.OwnBankCode)
	}
	if cfg.KafkaBrokers != "kafka:9092" {
		t.Errorf("override KafkaBrokers: got %s", cfg.KafkaBrokers)
	}
}

func TestDSN(t *testing.T) {
	t.Setenv("ACCOUNT_DB_HOST", "h")
	t.Setenv("ACCOUNT_DB_PORT", "5432")
	t.Setenv("ACCOUNT_DB_USER", "u")
	t.Setenv("ACCOUNT_DB_PASSWORD", "p")
	t.Setenv("ACCOUNT_DB_NAME", "n")
	t.Setenv("ACCOUNT_DB_SSLMODE", "require")

	cfg := Load()
	dsn := cfg.DSN()
	for _, want := range []string{"host=h", "port=5432", "user=u", "password=p", "dbname=n", "sslmode=require"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN missing %q: %s", want, dsn)
		}
	}
}
