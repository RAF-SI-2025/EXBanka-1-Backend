package config

import (
	"os"
)

type Config struct {
	HTTPAddr             string
	AuthGRPCAddr         string
	UserGRPCAddr         string
	ClientGRPCAddr       string
	AccountGRPCAddr      string
	CardGRPCAddr         string
	TransactionGRPCAddr  string
	CreditGRPCAddr       string
	ExchangeGRPCAddr     string
	StockGRPCAddr        string
	VerificationGRPCAddr string
	NotificationGRPCAddr string
	KafkaBrokers         string
	MetricsPort          string

	// Inter-bank HMAC (Spec 3 §8). The gateway needs Redis for the nonce
	// store and the per-peer plaintext inbound keys for HMAC verification.
	RedisAddr         string
	OwnBankCode       string
	Peer222InboundKey string
	Peer333InboundKey string
	Peer444InboundKey string
}

func Load() *Config {
	return &Config{
		HTTPAddr:             getEnv("GATEWAY_HTTP_ADDR", ":8080"),
		AuthGRPCAddr:         getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
		UserGRPCAddr:         getEnv("USER_GRPC_ADDR", "localhost:50052"),
		ClientGRPCAddr:       getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		AccountGRPCAddr:      getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		CardGRPCAddr:         getEnv("CARD_GRPC_ADDR", "localhost:50056"),
		TransactionGRPCAddr:  getEnv("TRANSACTION_GRPC_ADDR", "localhost:50057"),
		CreditGRPCAddr:       getEnv("CREDIT_GRPC_ADDR", "localhost:50058"),
		ExchangeGRPCAddr:     getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
		StockGRPCAddr:        getEnv("STOCK_GRPC_ADDR", "localhost:50060"),
		VerificationGRPCAddr: getEnv("VERIFICATION_GRPC_ADDR", "localhost:50061"),
		NotificationGRPCAddr: getEnv("NOTIFICATION_GRPC_ADDR", "localhost:50053"),
		KafkaBrokers:         getEnv("KAFKA_BROKERS", "localhost:9092"),
		MetricsPort:          getEnv("METRICS_PORT", "9100"),

		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		OwnBankCode:       getEnv("OWN_BANK_CODE", "111"),
		Peer222InboundKey: getEnv("PEER_222_INBOUND_KEY", ""),
		Peer333InboundKey: getEnv("PEER_333_INBOUND_KEY", ""),
		Peer444InboundKey: getEnv("PEER_444_INBOUND_KEY", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
