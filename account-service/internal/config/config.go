package config

import (
	"os"
)

type Config struct {
	DBHost         string
	DBPort         string
	DBUser         string
	DBPassword     string
	DBName         string
	GRPCAddr       string
	KafkaBrokers   string
	RedisAddr      string
	ClientGRPCAddr string
	MetricsPort    string
}

func Load() *Config {
	return &Config{
		DBHost:         getEnv("ACCOUNT_DB_HOST", "localhost"),
		DBPort:         getEnv("ACCOUNT_DB_PORT", "5435"),
		DBUser:         getEnv("ACCOUNT_DB_USER", "postgres"),
		DBPassword:     getEnv("ACCOUNT_DB_PASSWORD", "postgres"),
		DBName:         getEnv("ACCOUNT_DB_NAME", "accountdb"),
		GRPCAddr:       getEnv("ACCOUNT_GRPC_ADDR", ":50055"),
		KafkaBrokers:   getEnv("KAFKA_BROKERS", "localhost:9092"),
		RedisAddr:      getEnv("REDIS_ADDR", "localhost:6379"),
		ClientGRPCAddr: getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		MetricsPort:    getEnv("METRICS_PORT", "9105"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("ACCOUNT_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode +
		" TimeZone=UTC"
}
