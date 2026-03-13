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
		DBHost:       getEnv("TRANSACTION_DB_HOST", "localhost"),
		DBPort:       getEnv("TRANSACTION_DB_PORT", "5437"),
		DBUser:       getEnv("TRANSACTION_DB_USER", "postgres"),
		DBPassword:   getEnv("TRANSACTION_DB_PASSWORD", "postgres"),
		DBName:       getEnv("TRANSACTION_DB_NAME", "transactiondb"),
		GRPCAddr:     getEnv("TRANSACTION_GRPC_ADDR", ":50057"),
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
	sslmode := getEnv("TRANSACTION_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode
}
