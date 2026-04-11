package config

import (
	"os"
)

type Config struct {
	DBHost          string
	DBPort          string
	DBUser          string
	DBPassword      string
	DBName          string
	GRPCAddr        string
	KafkaBrokers    string
	AccountGRPCAddr string
	ClientGRPCAddr  string
	UserGRPCAddr    string
	MetricsPort     string
}

func Load() *Config {
	return &Config{
		DBHost:          getEnv("CREDIT_DB_HOST", "localhost"),
		DBPort:          getEnv("CREDIT_DB_PORT", "5438"),
		DBUser:          getEnv("CREDIT_DB_USER", "postgres"),
		DBPassword:      getEnv("CREDIT_DB_PASSWORD", "postgres"),
		DBName:          getEnv("CREDIT_DB_NAME", "creditdb"),
		GRPCAddr:        getEnv("CREDIT_GRPC_ADDR", ":50058"),
		KafkaBrokers:    getEnv("KAFKA_BROKERS", "localhost:9092"),
		AccountGRPCAddr: getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ClientGRPCAddr:  getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		UserGRPCAddr:    getEnv("USER_GRPC_ADDR", "localhost:50052"),
		MetricsPort:     getEnv("METRICS_PORT", "9108"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("CREDIT_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode +
		" TimeZone=UTC"
}
