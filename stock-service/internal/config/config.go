package config

import (
	"fmt"
	"os"
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
	// Dependencies
	UserGRPCAddr     string
	AccountGRPCAddr  string
	ExchangeGRPCAddr string
}

func Load() *Config {
	return &Config{
		DBHost:           getEnv("STOCK_DB_HOST", "localhost"),
		DBPort:           getEnv("STOCK_DB_PORT", "5440"),
		DBUser:           getEnv("STOCK_DB_USER", "postgres"),
		DBPassword:       getEnv("STOCK_DB_PASSWORD", "postgres"),
		DBName:           getEnv("STOCK_DB_NAME", "stock_db"),
		DBSslmode:        getEnv("STOCK_DB_SSLMODE", "disable"),
		GRPCAddr:         getEnv("STOCK_GRPC_ADDR", ":50060"),
		KafkaBrokers:     getEnv("KAFKA_BROKERS", "localhost:9092"),
		UserGRPCAddr:     getEnv("USER_GRPC_ADDR", "localhost:50052"),
		AccountGRPCAddr:  getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ExchangeGRPCAddr: getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
	}
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSslmode)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
