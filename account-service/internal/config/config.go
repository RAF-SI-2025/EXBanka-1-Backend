package config

import (
	"os"
	"time"
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
	OwnBankCode    string
	// OutgoingReservationTTL is how long a cross-bank money DEBIT hold may sit
	// pending (no COMMIT/ROLLBACK from the peer) before the timeout cron
	// releases it back to AvailableBalance. Time-safety backstop.
	OutgoingReservationTTL time.Duration
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
		OwnBankCode:    getEnv("OWN_BANK_CODE", "111"),
		OutgoingReservationTTL: getEnvDuration("OUTGOING_RESERVATION_TTL", 10*time.Minute),
	}
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
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
