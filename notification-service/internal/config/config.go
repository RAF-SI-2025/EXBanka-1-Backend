package config

import (
	"fmt"
	"os"
)

type Config struct {
	GRPCAddr     string
	KafkaBrokers string

	// SMTP
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// Database for mobile inbox
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	MetricsPort string
}

func Load() *Config {
	return &Config{
		GRPCAddr:     getEnv("NOTIFICATION_GRPC_ADDR", ":50053"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		SMTPHost:     getEnv("SMTP_HOST", "smtp.gmail.com"),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		DBHost:       getEnv("NOTIFICATION_DB_HOST", "localhost"),
		DBPort:       getEnv("NOTIFICATION_DB_PORT", "5442"),
		DBUser:       getEnv("NOTIFICATION_DB_USER", "postgres"),
		DBPassword:   getEnv("NOTIFICATION_DB_PASSWORD", "postgres"),
		DBName:       getEnv("NOTIFICATION_DB_NAME", "notification_db"),
		MetricsPort:  getEnv("METRICS_PORT", "9103"),
	}
}

func (c *Config) DSN() string {
	sslmode := getEnv("NOTIFICATION_DB_SSLMODE", "disable")
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, sslmode)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
