package config

import (
	"os"

	"github.com/joho/godotenv"
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
}

func Load() *Config {
	for _, f := range []string{".env", "../.env", "../../.env", "../../../.env"} {
		if err := godotenv.Load(f); err == nil {
			break
		}
	}
	return &Config{
		GRPCAddr:     getEnv("NOTIFICATION_GRPC_ADDR", ":50053"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		SMTPHost:     getEnv("SMTP_HOST", "smtp.gmail.com"),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
