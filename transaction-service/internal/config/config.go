package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DBHost               string
	DBPort               string
	DBUser               string
	DBPassword           string
	DBName               string
	GRPCAddr             string
	KafkaBrokers         string
	AccountGRPCAddr      string
	ExchangeGRPCAddr     string
	VerificationGRPCAddr string
	MetricsPort          string

	// Inter-bank 2PC tuning (Spec 3 §9.1).
	InterbankPrepareTimeout      time.Duration
	InterbankCommitTimeout       time.Duration
	InterbankReceiverWait        time.Duration
	InterbankReconcileInterval   time.Duration
	InterbankReconcileMaxRetries int
	InterbankReconcileStaleAfter time.Duration

	// Per-peer endpoint + HMAC keys.
	OwnBankCode        string
	Peer222BaseURL     string
	Peer222InboundKey  string
	Peer222OutboundKey string
	Peer333BaseURL     string
	Peer333InboundKey  string
	Peer333OutboundKey string
	Peer444BaseURL     string
	Peer444InboundKey  string
	Peer444OutboundKey string
}

func Load() *Config {
	return &Config{
		DBHost:               getEnv("TRANSACTION_DB_HOST", "localhost"),
		DBPort:               getEnv("TRANSACTION_DB_PORT", "5437"),
		DBUser:               getEnv("TRANSACTION_DB_USER", "postgres"),
		DBPassword:           getEnv("TRANSACTION_DB_PASSWORD", "postgres"),
		DBName:               getEnv("TRANSACTION_DB_NAME", "transactiondb"),
		GRPCAddr:             getEnv("TRANSACTION_GRPC_ADDR", ":50057"),
		KafkaBrokers:         getEnv("KAFKA_BROKERS", "localhost:9092"),
		AccountGRPCAddr:      getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ExchangeGRPCAddr:     getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
		VerificationGRPCAddr: getEnv("VERIFICATION_GRPC_ADDR", "localhost:50061"),
		MetricsPort:          getEnv("METRICS_PORT", "9107"),

		InterbankPrepareTimeout:      getDuration("INTERBANK_PREPARE_TIMEOUT", 30*time.Second),
		InterbankCommitTimeout:       getDuration("INTERBANK_COMMIT_TIMEOUT", 30*time.Second),
		InterbankReceiverWait:        getDuration("INTERBANK_RECEIVER_WAIT", 90*time.Second),
		InterbankReconcileInterval:   getDuration("INTERBANK_RECONCILE_INTERVAL", 60*time.Second),
		InterbankReconcileMaxRetries: getInt("INTERBANK_RECONCILE_MAX_RETRIES", 10),
		InterbankReconcileStaleAfter: getDuration("INTERBANK_RECONCILE_STALE_AFTER", 24*time.Hour),

		OwnBankCode:        getEnv("OWN_BANK_CODE", "111"),
		Peer222BaseURL:     getEnv("PEER_222_BASE_URL", ""),
		Peer222InboundKey:  getEnv("PEER_222_INBOUND_KEY", ""),
		Peer222OutboundKey: getEnv("PEER_222_OUTBOUND_KEY", ""),
		Peer333BaseURL:     getEnv("PEER_333_BASE_URL", ""),
		Peer333InboundKey:  getEnv("PEER_333_INBOUND_KEY", ""),
		Peer333OutboundKey: getEnv("PEER_333_OUTBOUND_KEY", ""),
		Peer444BaseURL:     getEnv("PEER_444_BASE_URL", ""),
		Peer444InboundKey:  getEnv("PEER_444_INBOUND_KEY", ""),
		Peer444OutboundKey: getEnv("PEER_444_OUTBOUND_KEY", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("TRANSACTION_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode +
		" TimeZone=UTC"
}
