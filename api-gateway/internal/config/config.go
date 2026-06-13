package config

import (
	"os"
	"strconv"
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
	// InterbankGRPCAddr is the cross-bank SI-TX settlement service. As of the
	// 2026-06-07 cutover it is THE backend for the entire /cross-bank-protocol
	// surface: PeerTx (2PC), PeerBankAdmin (registry), PeerOTC (forwarded to
	// stock-service), PeerEgress, and PeerUser. The gateway dials all of these
	// here instead of transaction-service/stock-service.
	InterbankGRPCAddr string
	KafkaBrokers      string
	MetricsPort       string

	// RedisAddr is the address of the Redis instance used by the gateway.
	// Currently used by the SI-TX PeerNonceStore (HMAC nonce dedup window;
	// Phase 2 Task 14).
	RedisAddr string

	OwnBankCode string

	// OwnBankName is the human-readable display name returned by the
	// peer-facing GET /user/{rid}/{id} endpoint (SI-TX §3.7
	// bankDisplayName). Falls back to OwnBankCode when OWN_BANK_NAME is
	// unset.
	OwnBankName string

	// Rate limiting (Phase A). A value of 0 disables that bucket.
	// Global is a generous per-IP safety ceiling across ALL routes — sized
	// well above the frontend's ~1s multi-route polling so normal traffic is
	// never throttled. Login/Reset are strict per-IP buckets on the two
	// brute-force-prone auth routes.
	RateLimitGlobalPerMin int
	RateLimitLoginPer5Min int
	RateLimitResetPer5Min int
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
		InterbankGRPCAddr:    getEnv("INTERBANK_GRPC_ADDR", "localhost:50062"),
		KafkaBrokers:         getEnv("KAFKA_BROKERS", "localhost:9092"),
		MetricsPort:          getEnv("METRICS_PORT", "9100"),

		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),

		OwnBankCode: getEnv("OWN_BANK_CODE", "111"),
		// Default to the bank code when no display name is configured.
		OwnBankName: getEnv("OWN_BANK_NAME", getEnv("OWN_BANK_CODE", "111")),

		RateLimitGlobalPerMin: getEnvInt("RATE_LIMIT_GLOBAL_PER_MIN", 3000),
		RateLimitLoginPer5Min: getEnvInt("RATE_LIMIT_LOGIN_PER_5MIN", 20),
		RateLimitResetPer5Min: getEnvInt("RATE_LIMIT_RESET_PER_5MIN", 5),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
