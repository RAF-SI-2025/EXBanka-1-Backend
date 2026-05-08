package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"VERIFICATION_DB_HOST", "VERIFICATION_DB_PORT", "VERIFICATION_DB_USER",
		"VERIFICATION_DB_PASSWORD", "VERIFICATION_DB_NAME", "VERIFICATION_DB_SSLMODE",
		"VERIFICATION_GRPC_ADDR", "KAFKA_BROKERS", "VERIFICATION_CHALLENGE_EXPIRY",
		"VERIFICATION_MAX_ATTEMPTS", "METRICS_PORT", "AUTH_GRPC_ADDR",
	} {
		_ = os.Unsetenv(k)
	}
	cfg := Load()
	if cfg.DBHost != "localhost" || cfg.DBPort != "5441" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.ChallengeExpiry != 5*time.Minute || cfg.MaxAttempts != 3 {
		t.Fatalf("expiry/attempts: %v %d", cfg.ChallengeExpiry, cfg.MaxAttempts)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("VERIFICATION_CHALLENGE_EXPIRY", "30s")
	t.Setenv("VERIFICATION_MAX_ATTEMPTS", "7")
	t.Setenv("VERIFICATION_DB_HOST", "h")
	cfg := Load()
	if cfg.ChallengeExpiry != 30*time.Second || cfg.MaxAttempts != 7 || cfg.DBHost != "h" {
		t.Fatalf("override: %+v", cfg)
	}
}

func TestLoadGarbageOverridesFallBack(t *testing.T) {
	t.Setenv("VERIFICATION_CHALLENGE_EXPIRY", "garbage")
	t.Setenv("VERIFICATION_MAX_ATTEMPTS", "garbage")
	cfg := Load()
	if cfg.ChallengeExpiry != 5*time.Minute || cfg.MaxAttempts != 3 {
		t.Fatalf("should fall back: %+v", cfg)
	}
	t.Setenv("VERIFICATION_MAX_ATTEMPTS", "0")
	cfg = Load()
	if cfg.MaxAttempts != 3 {
		t.Fatalf("zero should fall back to default 3, got %d", cfg.MaxAttempts)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("X_VS_K", "v")
	if getEnv("X_VS_K", "fb") != "v" {
		t.Fatal("override")
	}
	_ = os.Unsetenv("X_VS_K_M")
	if getEnv("X_VS_K_M", "fb") != "fb" {
		t.Fatal("fallback")
	}
}

func TestDSN(t *testing.T) {
	cfg := &Config{DBHost: "h", DBPort: "1", DBUser: "u", DBPassword: "p", DBName: "d", DBSslmode: "disable"}
	dsn := cfg.DSN()
	for _, want := range []string{"host=h", "port=1", "user=u", "password=p", "dbname=d", "sslmode=disable"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("dsn missing %q: %s", want, dsn)
		}
	}
}
