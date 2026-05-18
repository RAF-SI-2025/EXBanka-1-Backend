package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"NOTIFICATION_GRPC_ADDR", "KAFKA_BROKERS", "SMTP_HOST", "SMTP_PORT",
		"SMTP_USER", "SMTP_PASSWORD", "SMTP_FROM",
		"NOTIFICATION_DB_HOST", "NOTIFICATION_DB_PORT", "NOTIFICATION_DB_USER",
		"NOTIFICATION_DB_PASSWORD", "NOTIFICATION_DB_NAME",
		"NOTIFICATION_DB_SSLMODE", "METRICS_PORT",
	} {
		_ = os.Unsetenv(k)
	}
	cfg := Load()
	if cfg.GRPCAddr != ":50053" || cfg.SMTPHost != "smtp.gmail.com" {
		t.Fatalf("defaults: %+v", cfg)
	}
	if cfg.DBHost != "localhost" || cfg.DBName != "notification_db" {
		t.Fatalf("db defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("SMTP_USER", "user@x")
	t.Setenv("SMTP_FROM", "from@x")
	t.Setenv("NOTIFICATION_DB_HOST", "h")
	cfg := Load()
	if cfg.SMTPUser != "user@x" || cfg.SMTPFrom != "from@x" || cfg.DBHost != "h" {
		t.Fatalf("override: %+v", cfg)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("X_NS_K", "v")
	if getEnv("X_NS_K", "fb") != "v" {
		t.Fatal("override")
	}
	_ = os.Unsetenv("X_NS_K_M")
	if getEnv("X_NS_K_M", "fb") != "fb" {
		t.Fatal("fallback")
	}
}

func TestDSN(t *testing.T) {
	cfg := &Config{DBHost: "h", DBPort: "1", DBUser: "u", DBPassword: "p", DBName: "d"}
	dsn := cfg.DSN()
	for _, want := range []string{"host=h", "port=1", "dbname=d", "sslmode=disable"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("dsn missing %q: %s", want, dsn)
		}
	}
	t.Setenv("NOTIFICATION_DB_SSLMODE", "require")
	dsn2 := cfg.DSN()
	if !strings.Contains(dsn2, "sslmode=require") {
		t.Fatalf("dsn override missing: %s", dsn2)
	}
}
