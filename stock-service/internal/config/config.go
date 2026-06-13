package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	DBSslmode    string
	GRPCAddr     string
	KafkaBrokers string
	// Dependencies
	UserGRPCAddr     string
	AccountGRPCAddr  string
	ExchangeGRPCAddr string
	ClientGRPCAddr   string
	ExchangeCSVPath  string
	// Securities sync
	AlphaVantageAPIKey       string
	SecuritySyncIntervalMins int
	// Tax
	StateAccountNo string
	// External API keys
	EODHDAPIKey     string
	AlpacaAPIKey    string
	AlpacaAPISecret string
	FinnhubAPIKey   string
	RedisAddr       string
	MetricsPort     string
	// Market simulator
	MarketSimulatorURL string
	BankName           string
	// InfluxDB (time-series)
	InfluxURL    string
	InfluxToken  string
	InfluxOrg    string
	InfluxBucket string
	// OTC option-contract / offer expiry cron (Celina-4 / Spec 2).
	OTCExpiryCronUTC   string // "HH:MM" UTC; default 02:00
	OTCExpiryBatchSize int    // default 500
	// OTCExpiryWarningDays > 0 enables the expiring-soon warning N days before
	// settlement (SP5 E). Default 3; 0 disables.
	OTCExpiryWarningDays int
	// Cross-bank wiring. As of the 2026-06-07 cutover, stock-service no longer
	// dials transaction-service for cross-bank work — interbank-service hosts
	// the SI-TX engine (PeerTxService.InitiateOutboundTxWithPostings for OTC
	// settlement), the peer_banks registry (PeerBankAdminService), and the
	// single outbound HTTP egress (PeerEgressService.ProxyToPeer). All three
	// are reached over InterbankGRPCAddr. OwnBankCode is the local 3-digit
	// bank code used for cross-bank routing decisions.
	InterbankGRPCAddr string // default "localhost:50062"
	OwnBankCode       string // default "111"
	// WatchlistNotificationCronHours is how often (in hours) the daily watchlist
	// notification cron runs. Default 24 h.
	WatchlistNotificationCronHours int
	// SP3 fund statistics: daily NAV-snapshot cron time and the minimum number
	// of monthly returns required before metrics are shown.
	FundSnapshotCronUTC          string // "HH:MM" UTC; default 23:50
	FundMetricsMinMonthlyReturns int    // default 2
}

func Load() *Config {
	syncMins := 1
	if v := os.Getenv("SECURITY_SYNC_INTERVAL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			syncMins = n
		}
	}
	return &Config{
		DBHost:                         getEnv("STOCK_DB_HOST", "localhost"),
		DBPort:                         getEnv("STOCK_DB_PORT", "5440"),
		DBUser:                         getEnv("STOCK_DB_USER", "postgres"),
		DBPassword:                     getEnv("STOCK_DB_PASSWORD", "postgres"),
		DBName:                         getEnv("STOCK_DB_NAME", "stock_db"),
		DBSslmode:                      getEnv("STOCK_DB_SSLMODE", "disable"),
		GRPCAddr:                       getEnv("STOCK_GRPC_ADDR", ":50060"),
		KafkaBrokers:                   getEnv("KAFKA_BROKERS", "localhost:9092"),
		UserGRPCAddr:                   getEnv("USER_GRPC_ADDR", "localhost:50052"),
		AccountGRPCAddr:                getEnv("ACCOUNT_GRPC_ADDR", "localhost:50055"),
		ExchangeGRPCAddr:               getEnv("EXCHANGE_GRPC_ADDR", "localhost:50059"),
		ClientGRPCAddr:                 getEnv("CLIENT_GRPC_ADDR", "localhost:50054"),
		ExchangeCSVPath:                getEnv("EXCHANGE_CSV_PATH", "data/exchanges.csv"),
		AlphaVantageAPIKey:             getEnv("ALPHAVANTAGE_API_KEY", ""),
		SecuritySyncIntervalMins:       syncMins,
		StateAccountNo:                 getEnv("STATE_ACCOUNT_NUMBER", "0000000000000099"),
		EODHDAPIKey:                    getEnv("EODHD_API_KEY", ""),
		AlpacaAPIKey:                   getEnv("ALPACA_API_KEY", ""),
		AlpacaAPISecret:                getEnv("ALPACA_API_SECRET", ""),
		FinnhubAPIKey:                  getEnv("FINNHUB_API_KEY", ""),
		RedisAddr:                      getEnv("REDIS_ADDR", "localhost:6379"),
		MetricsPort:                    getEnv("METRICS_PORT", "9110"),
		MarketSimulatorURL:             getEnv("MARKET_SIMULATOR_URL", "http://localhost:8090"),
		BankName:                       getEnv("BANK_NAME", "ExBanka"),
		InfluxURL:                      getEnv("INFLUX_URL", ""),
		InfluxToken:                    getEnv("INFLUX_TOKEN", ""),
		InfluxOrg:                      getEnv("INFLUX_ORG", "exbanka"),
		InfluxBucket:                   getEnv("INFLUX_BUCKET", "stock_prices"),
		OTCExpiryCronUTC:               getEnv("OTC_EXPIRY_CRON_UTC", "02:00"),
		OTCExpiryBatchSize:             getEnvInt("OTC_EXPIRY_BATCH_SIZE", 500),
		OTCExpiryWarningDays:           getEnvInt("OTC_EXPIRY_WARNING_DAYS", 3),
		InterbankGRPCAddr:              getEnv("INTERBANK_GRPC_ADDR", "localhost:50062"),
		OwnBankCode:                    getEnv("OWN_BANK_CODE", "111"),
		WatchlistNotificationCronHours: getEnvInt("WATCHLIST_NOTIFICATION_CRON_HOURS", 24),
		FundSnapshotCronUTC:            getEnv("FUND_SNAPSHOT_CRON_UTC", "23:50"),
		FundMetricsMinMonthlyReturns:   getEnvInt("FUND_METRICS_MIN_MONTHLY_RETURNS", 2),
	}
}

// getEnvInt reads an env var as int, falling back to fallback on missing /
// invalid input.
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSslmode)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
