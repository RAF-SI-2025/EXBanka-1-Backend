package config

import "os"

// Config holds test environment configuration.
type Config struct {
	GatewayURL   string
	KafkaBrokers string
}

// Load reads configuration from environment variables with defaults.
func Load() *Config {
	return &Config{
		GatewayURL:   getEnv("TEST_GATEWAY_URL", "http://localhost:8080"),
		KafkaBrokers: getEnv("TEST_KAFKA_BROKERS", "localhost:9092"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
