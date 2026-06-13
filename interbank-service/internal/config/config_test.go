package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/exbanka/interbank-service/internal/config"
)

// TestLoad_Defaults verifies Load() applies the documented fallbacks when no
// environment variables are set.
func TestLoad_Defaults(t *testing.T) {
	// Clear every variable Load reads so the fallbacks are exercised.
	for _, k := range []string{
		"INTERBANK_DB_HOST", "INTERBANK_DB_PORT", "INTERBANK_DB_USER",
		"INTERBANK_DB_PASSWORD", "INTERBANK_DB_NAME", "INTERBANK_GRPC_ADDR",
		"METRICS_PORT", "ACCOUNT_GRPC_ADDR", "STOCK_GRPC_ADDR",
		"EXCHANGE_GRPC_ADDR", "CLIENT_GRPC_ADDR", "USER_GRPC_ADDR",
		"OWN_BANK_CODE", "OWN_BANK_DISPLAY_NAME", "SITX_RECEIVE_SYNC_DEADLINE",
	} {
		t.Setenv(k, "")
	}

	cfg := config.Load()

	if cfg.DBHost != "localhost" || cfg.DBPort != "5443" || cfg.DBUser != "postgres" {
		t.Errorf("db defaults: host=%q port=%q user=%q", cfg.DBHost, cfg.DBPort, cfg.DBUser)
	}
	if cfg.DBName != "interbankdb" {
		t.Errorf("db name default: %q", cfg.DBName)
	}
	if cfg.GRPCAddr != ":50062" || cfg.MetricsPort != "9112" {
		t.Errorf("listen defaults: grpc=%q metrics=%q", cfg.GRPCAddr, cfg.MetricsPort)
	}
	if cfg.AccountGRPCAddr != "localhost:50055" || cfg.StockGRPCAddr != "localhost:50060" {
		t.Errorf("downstream defaults: account=%q stock=%q", cfg.AccountGRPCAddr, cfg.StockGRPCAddr)
	}
	if cfg.ExchangeGRPCAddr != "localhost:50059" || cfg.ClientGRPCAddr != "localhost:50054" || cfg.UserGRPCAddr != "localhost:50052" {
		t.Errorf("downstream defaults: exchange=%q client=%q user=%q", cfg.ExchangeGRPCAddr, cfg.ClientGRPCAddr, cfg.UserGRPCAddr)
	}
	if cfg.OwnBankCode != "111" || cfg.OwnBankDisplayName != "EXBanka" {
		t.Errorf("identity defaults: code=%q name=%q", cfg.OwnBankCode, cfg.OwnBankDisplayName)
	}
	if cfg.ReceiveSyncDeadline != 5*time.Second {
		t.Errorf("deadline default: %v", cfg.ReceiveSyncDeadline)
	}
}

// TestLoad_Overrides verifies every variable is read from the environment when
// set, including a valid duration parse for the sync deadline.
func TestLoad_Overrides(t *testing.T) {
	t.Setenv("INTERBANK_DB_HOST", "db.example")
	t.Setenv("INTERBANK_DB_PORT", "6000")
	t.Setenv("INTERBANK_DB_USER", "ib")
	t.Setenv("INTERBANK_DB_PASSWORD", "secret")
	t.Setenv("INTERBANK_DB_NAME", "ibdb")
	t.Setenv("INTERBANK_GRPC_ADDR", ":1")
	t.Setenv("METRICS_PORT", "2")
	t.Setenv("ACCOUNT_GRPC_ADDR", "acct:1")
	t.Setenv("STOCK_GRPC_ADDR", "stock:1")
	t.Setenv("EXCHANGE_GRPC_ADDR", "ex:1")
	t.Setenv("CLIENT_GRPC_ADDR", "client:1")
	t.Setenv("USER_GRPC_ADDR", "user:1")
	t.Setenv("OWN_BANK_CODE", "444")
	t.Setenv("OWN_BANK_DISPLAY_NAME", "Banka 4")
	t.Setenv("SITX_RECEIVE_SYNC_DEADLINE", "2500ms")

	cfg := config.Load()

	if cfg.DBHost != "db.example" || cfg.DBPort != "6000" || cfg.DBUser != "ib" ||
		cfg.DBPassword != "secret" || cfg.DBName != "ibdb" {
		t.Errorf("db overrides not applied: %+v", cfg)
	}
	if cfg.GRPCAddr != ":1" || cfg.MetricsPort != "2" {
		t.Errorf("listen overrides not applied: grpc=%q metrics=%q", cfg.GRPCAddr, cfg.MetricsPort)
	}
	if cfg.AccountGRPCAddr != "acct:1" || cfg.StockGRPCAddr != "stock:1" ||
		cfg.ExchangeGRPCAddr != "ex:1" || cfg.ClientGRPCAddr != "client:1" || cfg.UserGRPCAddr != "user:1" {
		t.Errorf("downstream overrides not applied: %+v", cfg)
	}
	if cfg.OwnBankCode != "444" || cfg.OwnBankDisplayName != "Banka 4" {
		t.Errorf("identity overrides: code=%q name=%q", cfg.OwnBankCode, cfg.OwnBankDisplayName)
	}
	if cfg.ReceiveSyncDeadline != 2500*time.Millisecond {
		t.Errorf("deadline override: got %v want 2.5s", cfg.ReceiveSyncDeadline)
	}
}

// TestLoad_InvalidDuration verifies an unparseable SITX_RECEIVE_SYNC_DEADLINE
// falls back to the 5s default rather than erroring.
func TestLoad_InvalidDuration(t *testing.T) {
	t.Setenv("SITX_RECEIVE_SYNC_DEADLINE", "not-a-duration")
	cfg := config.Load()
	if cfg.ReceiveSyncDeadline != 5*time.Second {
		t.Errorf("invalid duration should fall back to 5s, got %v", cfg.ReceiveSyncDeadline)
	}
}

// TestDSN_BuildsConnString verifies DSN composes a GORM Postgres connection
// string from the config fields, honouring INTERBANK_DB_SSLMODE.
func TestDSN_BuildsConnString(t *testing.T) {
	t.Setenv("INTERBANK_DB_SSLMODE", "require")
	cfg := &config.Config{
		DBHost: "h", DBPort: "5443", DBUser: "u", DBPassword: "p", DBName: "n",
	}
	dsn := cfg.DSN()
	for _, want := range []string{
		"host=h", "port=5443", "user=u", "password=p", "dbname=n",
		"sslmode=require", "TimeZone=UTC",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN missing %q: %s", want, dsn)
		}
	}
}

// TestDSN_DefaultSSLMode verifies the sslmode default is "disable" when the
// env var is unset.
func TestDSN_DefaultSSLMode(t *testing.T) {
	t.Setenv("INTERBANK_DB_SSLMODE", "")
	cfg := &config.Config{DBHost: "h", DBPort: "1", DBUser: "u", DBPassword: "p", DBName: "n"}
	if dsn := cfg.DSN(); !strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("expected sslmode=disable, got %s", dsn)
	}
}
