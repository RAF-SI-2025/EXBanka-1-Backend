package main

import (
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

	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/client-service/internal/cache"
	"github.com/exbanka/client-service/internal/config"
	"github.com/exbanka/client-service/internal/handler"
	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/client-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Client{}, &model.ClientLimit{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		"client.created",
		"client.updated",
		"client.limits-updated",
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

	clientService := service.NewClientService(repo, producer, redisCache)
	clientLimitSvc := service.NewClientLimitService(clientLimitRepo, userLimitClient, producer)

	grpcHandler := handler.NewClientGRPCHandler(clientService)
	limitHandler := handler.NewClientLimitGRPCHandler(clientLimitSvc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	clientpb.RegisterClientServiceServer(s, grpcHandler)
	clientpb.RegisterClientLimitServiceServer(s, limitHandler)
	shared.RegisterHealthCheck(s, "client-service")

	// Start gRPC server in goroutine
	go func() {
		fmt.Printf("client service listening on %s\n", cfg.GRPCAddr)
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

// Keep os import used
var _ = os.Getenv
