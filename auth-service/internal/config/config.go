package config

import (
	"os"
	"time"
)

type Config struct {
	DBHost          string
	DBPort          string
	DBUser          string
	DBPassword      string
	DBName          string
	GRPCAddr        string
	UserGRPCAddr    string
	KafkaBrokers    string
	JWTSecret       string
	AccessExpiry    time.Duration
	RefreshExpiry   time.Duration
	RedisAddr       string
	FrontendBaseURL string
}

func Load() *Config {
	accessExp, _ := time.ParseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m"))
	refreshExp, _ := time.ParseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h"))

	return &Config{
		DBHost:        getEnv("AUTH_DB_HOST", "localhost"),
		DBPort:        getEnv("AUTH_DB_PORT", "5432"),
		DBUser:        getEnv("AUTH_DB_USER", "postgres"),
		DBPassword:    getEnv("AUTH_DB_PASSWORD", "postgres"),
		DBName:        getEnv("AUTH_DB_NAME", "authdb"),
		GRPCAddr:     getEnv("AUTH_GRPC_ADDR", ":50051"),
		UserGRPCAddr: getEnv("USER_GRPC_ADDR", "localhost:50052"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		JWTSecret:     getEnv("JWT_SECRET", "change-me"),
		AccessExpiry:  accessExp,
		RefreshExpiry: refreshExp,
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		FrontendBaseURL: getEnv("FRONTEND_BASE_URL", "http://localhost:3000"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("AUTH_DB_SSLMODE", "require")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode
}
