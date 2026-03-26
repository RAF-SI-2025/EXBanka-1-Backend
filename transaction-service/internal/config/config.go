package config

import (
	"os"
)

type Config struct {
	DBHost           string
	DBPort           string
	DBUser           string
	DBPassword       string
	DBName           string
	GRPCAddr         string
	KafkaBrokers     string
	RedisAddr        string
	AccountGRPCAddr  string
	ExchangeGRPCAddr string
}

func Load() *Config {
	return &Config{
		DBHost:           getEnv("TRANSACTION_DB_HOST", "localhost"),
		DBPort:           getEnv("TRANSACTION_DB_PORT", "5437"),
		DBUser:           getEnv("TRANSACTION_DB_USER", "postgres"),
		DBPassword:       getEnv("TRANSACTION_DB_PASSWORD", "postgres"),
		DBName:           getEnv("TRANSACTION_DB_NAME", "transactiondb"),
		GRPCAddr:         getEnv("TRANSACTION_GRPC_ADDR", ":50057"),
		KafkaBrokers:     getEnv("KAFKA_BROKERS", "localhost:9092"),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		AccountGRPCAddr:  getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ExchangeGRPCAddr: getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
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
