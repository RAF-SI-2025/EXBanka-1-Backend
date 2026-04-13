package config

import (
	"os"
	"strconv"
)

type Config struct {
	DBHost            string
	DBPort            string
	DBUser            string
	DBPassword        string
	DBName            string
	GRPCAddr          string
	KafkaBrokers      string
	APIKey            string // EXCHANGE_API_KEY — empty = use free open tier
	CommissionRate    string // EXCHANGE_COMMISSION_RATE — default "0.005" (0.5%)
	Spread            string // EXCHANGE_SPREAD — default "0.003" (0.3%), applied to derive buy/sell from mid
	SyncIntervalHours int
	RedisAddr         string
	MetricsPort       string
}

func Load() *Config {
	syncHours := 6
	if v := os.Getenv("EXCHANGE_SYNC_INTERVAL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			syncHours = n
		}
	}
	return &Config{
		DBHost:            getEnv("EXCHANGE_DB_HOST", "localhost"),
		DBPort:            getEnv("EXCHANGE_DB_PORT", "5439"),
		DBUser:            getEnv("EXCHANGE_DB_USER", "postgres"),
		DBPassword:        getEnv("EXCHANGE_DB_PASSWORD", "postgres"),
		DBName:            getEnv("EXCHANGE_DB_NAME", "exchangedb"),
		GRPCAddr:          getEnv("EXCHANGE_GRPC_ADDR", ":50059"),
		KafkaBrokers:      getEnv("KAFKA_BROKERS", "localhost:9092"),
		APIKey:            getEnv("EXCHANGE_API_KEY", ""),
		CommissionRate:    getEnv("EXCHANGE_COMMISSION_RATE", "0.005"),
		Spread:            getEnv("EXCHANGE_SPREAD", "0.003"),
		SyncIntervalHours: syncHours,
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		MetricsPort:       getEnv("METRICS_PORT", "9109"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("EXCHANGE_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode +
		" TimeZone=UTC"
}
