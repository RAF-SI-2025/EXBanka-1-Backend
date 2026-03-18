package config

import (
	"os"
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
	return &Config{
		DBHost:       getEnv("CLIENT_DB_HOST", "localhost"),
		DBPort:       getEnv("CLIENT_DB_PORT", "5434"),
		DBUser:       getEnv("CLIENT_DB_USER", "postgres"),
		DBPassword:   getEnv("CLIENT_DB_PASSWORD", "postgres"),
		DBName:       getEnv("CLIENT_DB_NAME", "clientdb"),
		GRPCAddr:     getEnv("CLIENT_GRPC_ADDR", ":50054"),
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
	sslmode := getEnv("CLIENT_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode
}
