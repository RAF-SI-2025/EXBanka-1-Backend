package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	GRPCAddr     string
	KafkaBrokers string
	RedisAddr    string
}

func Load() *Config {
	for _, f := range []string{".env", "../.env", "../../.env", "../../../.env"} {
		if err := godotenv.Load(f); err == nil {
			break
		}
	}
	return &Config{
		DBHost:       getEnv("USER_DB_HOST", "exteam1-database.postgres.database.azure.com"),
		DBPort:       getEnv("USER_DB_PORT", "5432"),
		DBUser:       getEnv("USER_DB_USER", "exteam1"),
		DBPassword:   getEnv("USER_DB_PASSWORD", "Anajankovic03"),
		DBName:       getEnv("USER_DB_NAME", "userservicedb"),
		GRPCAddr:     getEnv("USER_GRPC_ADDR", ":50052"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	sslmode := getEnv("USER_DB_SSLMODE", "require")
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + sslmode
}
