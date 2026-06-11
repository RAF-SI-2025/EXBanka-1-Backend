// Package config loads interbank-service settings from the environment.
package config

import (
	"os"
	"time"
)

// Config holds the interbank-service runtime configuration. The service is the
// cross-bank SI-TX settlement engine: it owns the peer-bank registry, the 2PC
// transport (peer HTTP egress), and the receiver/sender idempotency state. It
// depends on account-service (money legs) and stock-service (option legs) over
// gRPC, and on the api-gateway as the only HTTP↔gRPC translator for inbound
// peer traffic.
type Config struct {
	// Own DB (peer_banks, peer_idempotence_records, outbound_peer_txs).
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// gRPC listen address (business API: PeerTx + PeerBankAdmin + PeerEgress).
	GRPCAddr string
	// Ops HTTP port: /metrics, /livez, /readyz ONLY (no business REST).
	MetricsPort string

	// Downstream gRPC dependencies.
	AccountGRPCAddr string
	StockGRPCAddr   string
	// exchange-service, used by the posting executor for seller-side FX on
	// cross-currency OTC credits (premium/strike) so they land in the
	// recipient's own account currency instead of voting NO_SUCH_ACCOUNT.
	ExchangeGRPCAddr string
	// Forwarding targets for the inbound /cross-bank-protocol surface this
	// service fronts: OTC → stock-service; friendly-name /user → client+user.
	ClientGRPCAddr string
	UserGRPCAddr   string

	// Bank identity (3-digit routing prefix) + display name surfaced on /user.
	OwnBankCode        string
	OwnBankDisplayName string

	// Receiver-side 202-async deadline: how long HandleNewTx waits for the
	// background reserve worker before returning pending.
	ReceiveSyncDeadline time.Duration
}

// Load reads the configuration from the environment, applying defaults.
func Load() *Config {
	return &Config{
		DBHost:              getEnv("INTERBANK_DB_HOST", "localhost"),
		DBPort:              getEnv("INTERBANK_DB_PORT", "5443"),
		DBUser:              getEnv("INTERBANK_DB_USER", "postgres"),
		DBPassword:          getEnv("INTERBANK_DB_PASSWORD", "postgres"),
		DBName:              getEnv("INTERBANK_DB_NAME", "interbankdb"),
		GRPCAddr:            getEnv("INTERBANK_GRPC_ADDR", ":50062"),
		MetricsPort:         getEnv("METRICS_PORT", "9112"),
		AccountGRPCAddr:     getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		StockGRPCAddr:       getEnv("STOCK_GRPC_ADDR", "localhost:50060"),
		ExchangeGRPCAddr:    getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
		ClientGRPCAddr:      getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		UserGRPCAddr:        getEnv("USER_GRPC_ADDR", "localhost:50052"),
		OwnBankCode:         getEnv("OWN_BANK_CODE", "111"),
		OwnBankDisplayName:  getEnv("OWN_BANK_DISPLAY_NAME", "EXBanka"),
		ReceiveSyncDeadline: getDuration("SITX_RECEIVE_SYNC_DEADLINE", 5*time.Second),
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

// DSN builds the GORM Postgres connection string.
func (c *Config) DSN() string {
	sslmode := getEnv("INTERBANK_DB_SSLMODE", "disable")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode +
		" TimeZone=UTC"
}
