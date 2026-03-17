package main

import (
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/clientpb"
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
	if err := db.AutoMigrate(&model.Client{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	repo := repository.NewClientRepository(db)
	clientService := service.NewClientService(repo, producer, redisCache)
	grpcHandler := handler.NewClientGRPCHandler(clientService, producer)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterClientServiceServer(s, grpcHandler)

	fmt.Printf("client service listening on %s\n", cfg.GRPCAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
