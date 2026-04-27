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

	"github.com/exbanka/card-service/internal/cache"
	"github.com/exbanka/card-service/internal/config"
	"github.com/exbanka/card-service/internal/handler"
	kafkaprod "github.com/exbanka/card-service/internal/kafka"
	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Card{}, &model.AuthorizedPerson{}, &model.CardBlock{}, &model.CardRequest{}, &model.Changelog{}, &model.IdempotencyRecord{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"card.created",
		"card.status-changed",
		"card.temporary-blocked",
		"card.virtual-card-created",
		"card.request-created",
		"card.request-approved",
		"card.request-rejected",
		"card.changelog",
		"notification.send-email",
		"notification.general",
	)

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	// Connect to client-service
	clientConn, err := grpc.NewClient(cfg.ClientGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to client service: %v", err)
	}
	defer clientConn.Close()
	clientClient := clientpb.NewClientServiceClient(clientConn)

	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	authRepo := repository.NewAuthorizedPersonRepository(db)
	cardRequestRepo := repository.NewCardRequestRepository(db)
	changelogRepo := repository.NewChangelogRepository(db)
	cardService := service.NewCardService(cardRepo, blockRepo, authRepo, producer, redisCache, db, changelogRepo)
	cardRequestSvc := service.NewCardRequestService(cardRequestRepo, cardService, producer)
	grpcHandler := handler.NewCardGRPCHandler(cardService, producer, clientClient)
	virtualCardHandler := handler.NewVirtualCardGRPCHandler(cardService)
	cardRequestHandler := handler.NewCardRequestGRPCHandler(cardRequestSvc)

	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	service.StartCardCron(cronCtx, cardRepo, blockRepo, db)

	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	if err := shared.RunGRPCServer(cronCtx, shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(
				metrics.GRPCUnaryServerInterceptor(),
				grpcmw.UnaryLoggingInterceptor("card-service"),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterCardServiceServer(s, grpcHandler)
			pb.RegisterVirtualCardServiceServer(s, virtualCardHandler)
			pb.RegisterCardRequestServiceServer(s, cardRequestHandler)
			shared.RegisterHealthCheck(s, "card-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			fmt.Printf("card service listening on %s\n", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}
