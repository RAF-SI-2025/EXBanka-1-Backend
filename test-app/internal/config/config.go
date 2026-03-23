package config

import (
	"os"
	"strings"
)

// Config holds test environment configuration.
type Config struct {
	GatewayURL   string
	KafkaBrokers string
	testEmail    string
	Password     string
}

// Load reads configuration from environment variables with defaults.
func Load() *Config {
	return &Config{
		GatewayURL:   getEnv("TEST_GATEWAY_URL", "http://localhost:8080"),
		KafkaBrokers: getEnv("TEST_KAFKA_BROKERS", "localhost:9092"),
		testEmail:    getEnv("TEST_EMAIL", "exbankatest@gmail.com"),
		Password:     getEnv("TEST_PASSWORD", "Test1234!"),
	}
}

// AdminEmail returns the test email with +admin tag inserted before the @.
// e.g. exbankatest@gmail.com → exbankatest+admin@gmail.com
func (c *Config) AdminEmail() string {
	return buildTaggedEmail(c.testEmail, "admin")
}

// ClientEmail returns the test email with +client tag inserted before the @.
// e.g. exbankatest@gmail.com → exbankatest+client@gmail.com
func (c *Config) ClientEmail() string {
	return buildTaggedEmail(c.testEmail, "client")
}

// buildTaggedEmail inserts a +tag before the @ in an email address.
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
