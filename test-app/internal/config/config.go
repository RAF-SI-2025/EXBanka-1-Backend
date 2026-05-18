package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds test environment configuration.
type Config struct {
	GatewayURL    string
	KafkaBrokers  string
	AdminEmail    string // email of the seeder-created admin account
	AdminPassword string // password of the seeder-created admin account
	BaseEmail     string // base email used to derive tagged addresses for test accounts
	Password      string // password used when creating test accounts
}

// Load reads configuration from environment variables with defaults.
func Load() *Config {
	return &Config{
		GatewayURL:    getEnv("TEST_GATEWAY_URL", "http://localhost:8080"),
		KafkaBrokers:  getEnv("TEST_KAFKA_BROKERS", "localhost:9094"),
		AdminEmail:    getEnv("ADMIN_EMAIL", "admin+testadmin@admin.com"),
		AdminPassword: getEnv("ADMIN_PASSWORD", "AdminAdmin2026!."),
		BaseEmail:     getEnv("TEST_BASE_EMAIL", "lsavic12123rn+test@raf.rs"),
		Password:      "AdminAdmin2026!.",
	}
}

// ClientEmail returns the base email with a numbered +clientN tag.
// e.g. user@raf.rs + n=1 → user+client1@raf.rs
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
