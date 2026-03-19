package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/cache"
	"github.com/exbanka/transaction-service/internal/config"
	"github.com/exbanka/transaction-service/internal/handler"
	kafkaprod "github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	nbs "github.com/exbanka/transaction-service/internal/nbs"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := db.AutoMigrate(
		&model.Payment{},
		&model.Transfer{},
		&model.PaymentRecipient{},
		&model.VerificationCode{},
		&model.ExchangeRate{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	model.SeedExchangeRates(db)

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

	paymentRepo := repository.NewPaymentRepository(db)
	transferRepo := repository.NewTransferRepository(db)
	recipientRepo := repository.NewPaymentRecipientRepository(db)
	vcRepo := repository.NewVerificationCodeRepository(db)
	exchangeRepo := repository.NewExchangeRateRepository(db)

	exchangeSvc := service.NewExchangeService(exchangeRepo)

	// NBS exchange rate sync (every 6 hours)
	nbsClient := nbs.NewClient()
	// Initial sync on startup (log warning on failure, don't crash)
	if err := exchangeSvc.SyncFromNBS(context.Background(), nbsClient); err != nil {
		log.Printf("warn: initial NBS sync failed, using seed rates: %v", err)
	}
	// Periodic sync
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		for range ticker.C {
			if err := exchangeSvc.SyncFromNBS(context.Background(), nbsClient); err != nil {
				log.Printf("warn: periodic NBS sync failed: %v", err)
			}
		}
	}()

	paymentSvc := service.NewPaymentService(paymentRepo, accountClient)
	transferSvc := service.NewTransferService(transferRepo, exchangeSvc, accountClient)
	recipientSvc := service.NewPaymentRecipientService(recipientRepo)
	verificationSvc := service.NewVerificationService(vcRepo)

	grpcHandler := handler.NewTransactionGRPCHandler(
		paymentSvc,
		transferSvc,
		recipientSvc,
		exchangeSvc,
		verificationSvc,
		producer,
	)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterTransactionServiceServer(s, grpcHandler)
	shared.RegisterHealthCheck(s, "transaction-service")

	// Start gRPC server in goroutine
	go func() {
		fmt.Printf("transaction service listening on %s\n", cfg.GRPCAddr)
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
