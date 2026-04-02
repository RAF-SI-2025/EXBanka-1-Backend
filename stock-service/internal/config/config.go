package config

import (
	"fmt"
	"os"
	"strconv"
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
	ClientGRPCAddr   string
	ExchangeCSVPath  string
	// Securities sync
	AlphaVantageAPIKey       string
	SecuritySyncIntervalMins int
	// Tax
	StateAccountNo string
	// External API keys
	EODHDAPIKey     string
	AlpacaAPIKey    string
	AlpacaAPISecret string
	FinnhubAPIKey   string
}

func Load() *Config {
	syncMins := 15
	if v := os.Getenv("SECURITY_SYNC_INTERVAL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			syncMins = n
		}
	}
	return &Config{
		DBHost:                   getEnv("STOCK_DB_HOST", "localhost"),
		DBPort:                   getEnv("STOCK_DB_PORT", "5440"),
		DBUser:                   getEnv("STOCK_DB_USER", "postgres"),
		DBPassword:               getEnv("STOCK_DB_PASSWORD", "postgres"),
		DBName:                   getEnv("STOCK_DB_NAME", "stock_db"),
		DBSslmode:                getEnv("STOCK_DB_SSLMODE", "disable"),
		GRPCAddr:                 getEnv("STOCK_GRPC_ADDR", ":50060"),
		KafkaBrokers:             getEnv("KAFKA_BROKERS", "localhost:9092"),
		UserGRPCAddr:             getEnv("USER_GRPC_ADDR", "localhost:50052"),
		AccountGRPCAddr:          getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ExchangeGRPCAddr:         getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
		ClientGRPCAddr:           getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		ExchangeCSVPath:          getEnv("EXCHANGE_CSV_PATH", "data/exchanges.csv"),
		AlphaVantageAPIKey:       getEnv("ALPHAVANTAGE_API_KEY", ""),
		SecuritySyncIntervalMins: syncMins,
		StateAccountNo:           getEnv("STATE_ACCOUNT_NUMBER", "0000000000000099"),
		EODHDAPIKey:              getEnv("EODHD_API_KEY", ""),
		AlpacaAPIKey:             getEnv("ALPACA_API_KEY", ""),
		AlpacaAPISecret:          getEnv("ALPACA_API_SECRET", ""),
		FinnhubAPIKey:            getEnv("FINNHUB_API_KEY", ""),
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
