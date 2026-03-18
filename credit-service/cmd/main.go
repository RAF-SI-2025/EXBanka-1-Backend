package main

import (
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

	accountpb "github.com/exbanka/contract/accountpb"
	pb "github.com/exbanka/contract/creditpb"
	shared "github.com/exbanka/contract/shared"
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

	// Connect to account-service
	accountConn, err := grpc.NewClient(cfg.AccountGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to account service: %v", err)
	}
	defer accountConn.Close()
	accountClient := accountpb.NewAccountServiceClient(accountConn)

	loanRequestRepo := repository.NewLoanRequestRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installmentRepo := repository.NewInstallmentRepository(db)

	loanRequestSvc := service.NewLoanRequestService(loanRequestRepo, loanRepo, installmentRepo)
	loanSvc := service.NewLoanService(loanRepo)
	installmentSvc := service.NewInstallmentService(installmentRepo)
	cronSvc := service.NewCronService(installmentSvc, loanSvc, accountClient, producer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cronSvc.Start(ctx)

	grpcHandler := handler.NewCreditGRPCHandler(loanRequestSvc, loanSvc, installmentSvc, producer)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterCreditServiceServer(s, grpcHandler)
	shared.RegisterHealthCheck(s, "credit-service")

	// Start gRPC server in goroutine
	go func() {
		fmt.Printf("credit service listening on %s\n", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	cancel()
	s.GracefulStop()
	log.Println("Server stopped")
}
