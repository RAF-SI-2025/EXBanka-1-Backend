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
}

func Load() *Config {
	godotenv.Load()
	return &Config{
		DBHost:       getEnv("USER_DB_HOST", "localhost"),
		DBPort:       getEnv("USER_DB_PORT", "5432"),
		DBUser:       getEnv("USER_DB_USER", "postgres"),
		DBPassword:   getEnv("USER_DB_PASSWORD", "postgres"),
		DBName:       getEnv("USER_DB_NAME", "user_db"),
		GRPCAddr:     getEnv("USER_GRPC_ADDR", ":50052"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("USER_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode
}
