package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	GRPCAddr     string
	KafkaBrokers string
	RedisAddr    string
}

func Load() *Config {
	for _, f := range []string{".env", "../.env", "../../.env", "../../../.env"} {
		if err := godotenv.Load(f); err == nil {
			break
		}
	}
	return &Config{
		DBHost:       getEnv("CARD_DB_HOST", "localhost"),
		DBPort:       getEnv("CARD_DB_PORT", "5436"),
		DBUser:       getEnv("CARD_DB_USER", "postgres"),
		DBPassword:   getEnv("CARD_DB_PASSWORD", "postgres"),
		DBName:       getEnv("CARD_DB_NAME", "carddb"),
		GRPCAddr:     getEnv("CARD_GRPC_ADDR", ":50056"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("CARD_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode
}
