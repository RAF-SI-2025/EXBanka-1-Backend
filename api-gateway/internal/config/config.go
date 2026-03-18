package config

import (
	"os"
)

type Config struct {
	HTTPAddr           string
	AuthGRPCAddr       string
	UserGRPCAddr       string
	ClientGRPCAddr     string
	AccountGRPCAddr    string
	CardGRPCAddr       string
	TransactionGRPCAddr string
	CreditGRPCAddr     string
}

func Load() *Config {
	return &Config{
		HTTPAddr:            getEnv("GATEWAY_HTTP_ADDR", ":8080"),
		AuthGRPCAddr:        getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
		UserGRPCAddr:        getEnv("USER_GRPC_ADDR", "localhost:50052"),
		ClientGRPCAddr:      getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		AccountGRPCAddr:     getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		CardGRPCAddr:        getEnv("CARD_GRPC_ADDR", "localhost:50056"),
		TransactionGRPCAddr: getEnv("TRANSACTION_GRPC_ADDR", "localhost:50057"),
		CreditGRPCAddr:      getEnv("CREDIT_GRPC_ADDR", "localhost:50058"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
