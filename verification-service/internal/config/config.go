package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	DBSslmode    string
	GRPCAddr     string
	KafkaBrokers string

	ChallengeExpiry time.Duration
	MaxAttempts     int
	MetricsPort     string
	AuthGRPCAddr    string
}

func Load() *Config {
	expiry := 5 * time.Minute
	if v := os.Getenv("VERIFICATION_CHALLENGE_EXPIRY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			expiry = d
		}
	}

	maxAttempts := 3
	if v := os.Getenv("VERIFICATION_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAttempts = n
		}
	}

	return &Config{
		DBHost:          getEnv("VERIFICATION_DB_HOST", "localhost"),
		DBPort:          getEnv("VERIFICATION_DB_PORT", "5441"),
		DBUser:          getEnv("VERIFICATION_DB_USER", "postgres"),
		DBPassword:      getEnv("VERIFICATION_DB_PASSWORD", "postgres"),
		DBName:          getEnv("VERIFICATION_DB_NAME", "verificationdb"),
		DBSslmode:       getEnv("VERIFICATION_DB_SSLMODE", "disable"),
		GRPCAddr:        getEnv("VERIFICATION_GRPC_ADDR", ":50061"),
		KafkaBrokers:    getEnv("KAFKA_BROKERS", "localhost:9092"),
		ChallengeExpiry: expiry,
		MaxAttempts:     maxAttempts,
		MetricsPort:     getEnv("METRICS_PORT", "9111"),
		AuthGRPCAddr:    getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSslmode)
}
