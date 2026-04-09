package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/exchange-service/internal/cache"
	"github.com/exbanka/exchange-service/internal/config"
	"github.com/exbanka/exchange-service/internal/handler"
	kafkaprod "github.com/exbanka/exchange-service/internal/kafka"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/provider"
	"github.com/exbanka/exchange-service/internal/repository"
	"github.com/exbanka/exchange-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.ExchangeRate{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	kafkaprod.EnsureTopics(cfg.KafkaBrokers, "exchange.rates-updated")

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	repo := repository.NewExchangeRateRepository(db)
	svc, err := service.NewExchangeService(repo, db, cfg.CommissionRate, cfg.Spread, redisCache)
	if err != nil {
		log.Fatalf("failed to create exchange service: %v", err)
	}

	rateProvider := provider.NewExchangeRateAPIClient(cfg.APIKey, "")

	// Seed hardcoded defaults for any pairs not yet in the DB.
	// This guarantees the service is never left with an empty rate table even
	// if the external API is unreachable at first boot.
	model.SeedDefaultRates(repo)

	// Initial sync — non-fatal on failure (service starts with seed/stale DB).
	if err := svc.SyncRates(context.Background(), rateProvider); err != nil {
		log.Printf("WARN: initial rate sync failed, starting with cached/empty rates: %v", err)
	} else {
		log.Println("exchange-service: initial rate sync complete")
		if err := producer.PublishRatesUpdated(context.Background(), provider.SupportedCurrencies, time.Now().UTC().Format(time.RFC3339)); err != nil {
			log.Printf("WARN: failed to publish rates-updated event: %v", err)
		}
	}

	// Periodic sync every N hours.
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.SyncIntervalHours) * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := svc.SyncRates(context.Background(), rateProvider); err != nil {
				log.Printf("WARN: periodic rate sync failed: %v", err)
			} else {
				if err := producer.PublishRatesUpdated(context.Background(), provider.SupportedCurrencies, time.Now().UTC().Format(time.RFC3339)); err != nil {
					log.Printf("WARN: failed to publish rates-updated event: %v", err)
				}
			}
		}
	}()

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
	pb.RegisterExchangeServiceServer(s, handler.NewExchangeGRPCHandler(svc))
	shared.RegisterHealthCheck(s, "exchange-service")
	metrics.InitializeGRPCMetrics(s)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	markReady()
	go func() {
		log.Printf("exchange-service listening on %s", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down exchange-service gracefully...")
	s.GracefulStop()
	log.Println("Server stopped")
}
