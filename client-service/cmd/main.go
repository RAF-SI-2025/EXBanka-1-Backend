package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/cache"
	"github.com/exbanka/client-service/internal/config"
	"github.com/exbanka/client-service/internal/handler"
	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/client-service/internal/service"
	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	userpb "github.com/exbanka/contract/userpb"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Client{}, &model.ClientLimit{}, &model.Changelog{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"client.created",
		"client.updated",
		"client.limits-updated",
		"client.changelog",
		"notification.send-email",
	)

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	// Connect to user-service for employee limit enforcement
	var userLimitClient userpb.EmployeeLimitServiceClient
	userConn, userErr := grpc.NewClient(cfg.UserGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if userErr != nil {
		log.Printf("warn: failed to connect to user service for limit enforcement: %v", userErr)
	} else {
		defer userConn.Close()
		userLimitClient = userpb.NewEmployeeLimitServiceClient(userConn)
	}

	repo := repository.NewClientRepository(db)
	clientLimitRepo := repository.NewClientLimitRepository(db)
	changelogRepo := repository.NewChangelogRepository(db)

	clientService := service.NewClientService(repo, producer, redisCache, changelogRepo)
	clientLimitSvc := service.NewClientLimitService(clientLimitRepo, userLimitClient, producer, changelogRepo)

	grpcHandler := handler.NewClientGRPCHandler(clientService)
	limitHandler := handler.NewClientLimitGRPCHandler(clientLimitSvc)

	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	if err := shared.RunGRPCServer(context.Background(), shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			clientpb.RegisterClientServiceServer(s, grpcHandler)
			clientpb.RegisterClientLimitServiceServer(s, limitHandler)
			shared.RegisterHealthCheck(s, "client-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			fmt.Printf("client service listening on %s\n", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}
