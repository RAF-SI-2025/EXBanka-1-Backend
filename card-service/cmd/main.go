package main

import (
	"time"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

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
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Card{}, &model.AuthorizedPerson{}, &model.CardBlock{}, &model.CardRequest{}, &model.Changelog{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
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

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
	pb.RegisterCardServiceServer(s, grpcHandler)
	pb.RegisterVirtualCardServiceServer(s, virtualCardHandler)
	pb.RegisterCardRequestServiceServer(s, cardRequestHandler)
	shared.RegisterHealthCheck(s, "card-service")
	metrics.InitializeGRPCMetrics(s)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// Start gRPC server in goroutine
	markReady()
	go func() {
		fmt.Printf("card service listening on %s\n", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	s.GracefulStop()
	log.Println("Server stopped")
}
