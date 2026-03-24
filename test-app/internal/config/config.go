package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds test environment configuration.
type Config struct {
	GatewayURL      string
	KafkaBrokers    string
	BaseEmail       string // base email used to derive tagged addresses
	Password        string
	BootstrapSecret string
}

// Load reads configuration from environment variables with defaults.
func Load() *Config {
	return &Config{
		GatewayURL:      getEnv("TEST_GATEWAY_URL", "http://localhost:8080"),
		KafkaBrokers:    getEnv("TEST_KAFKA_BROKERS", "localhost:9094"),
		BaseEmail:       getEnv("TEST_BASE_EMAIL", "lsavic12123rn@raf.rs"),
		Password:        "AdminAdmin2026!.",
		BootstrapSecret: getEnv("BOOTSTRAP_SECRET", "dev-bootstrap-secret"),
	}
}

// AdminEmail returns the base email with the +admin tag.
// e.g. vlupsic11723rn@raf.rs → vlupsic11723rn+admin@raf.rs
// This is the address used by the seeded admin employee account.
func (c *Config) AdminEmail() string {
	return buildTaggedEmail(c.BaseEmail, "admin")
}

// ClientEmail returns the base email with a numbered +clientN tag.
// e.g. vlupsic11723rn@raf.rs + n=1 → vlupsic11723rn+client1@raf.rs
// Use sequential numbers across tests to avoid email collisions.
func (c *Config) ClientEmail(n int) string {
	return buildTaggedEmail(c.BaseEmail, fmt.Sprintf("client%d", n))
}

// buildTaggedEmail inserts +tag before the @ in an email address.
func buildTaggedEmail(base, tag string) string {
	at := strings.Index(base, "@")
	if at < 0 {
		return base
	}
	return base[:at] + "+" + tag + base[at:]
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
