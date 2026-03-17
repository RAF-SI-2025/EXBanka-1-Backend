package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/creditpb"
	"github.com/exbanka/credit-service/internal/cache"
	"github.com/exbanka/credit-service/internal/config"
	"github.com/exbanka/credit-service/internal/handler"
	kafkaprod "github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/exbanka/credit-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.LoanRequest{}, &model.Loan{}, &model.Installment{}); err != nil {
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

	loanRequestRepo := repository.NewLoanRequestRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installmentRepo := repository.NewInstallmentRepository(db)

	loanRequestSvc := service.NewLoanRequestService(loanRequestRepo, loanRepo, installmentRepo)
	loanSvc := service.NewLoanService(loanRepo)
	installmentSvc := service.NewInstallmentService(installmentRepo)
	cronSvc := service.NewCronService(installmentSvc, loanSvc)

	ctx := context.Background()
	go cronSvc.Start(ctx)

	grpcHandler := handler.NewCreditGRPCHandler(loanRequestSvc, loanSvc, installmentSvc, producer)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterCreditServiceServer(s, grpcHandler)

	fmt.Printf("credit service listening on %s\n", cfg.GRPCAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
