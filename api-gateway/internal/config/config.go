package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr     string
	AuthGRPCAddr string
	UserGRPCAddr string
}

func Load() *Config {
	for _, f := range []string{".env", "../.env", "../../.env", "../../../.env"} {
		if err := godotenv.Load(f); err == nil {
			break
		}
	}
	return &Config{
		HTTPAddr:     getEnv("GATEWAY_HTTP_ADDR", ":8080"),
		AuthGRPCAddr: getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
		UserGRPCAddr: getEnv("USER_GRPC_ADDR", "localhost:50052"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
