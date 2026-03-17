package main

import (
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/cache"
	"github.com/exbanka/transaction-service/internal/config"
	"github.com/exbanka/transaction-service/internal/handler"
	kafkaprod "github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
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

	if err := model.SeedExchangeRates(db); err != nil {
		log.Printf("warn: seed exchange rates: %v", err)
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

	paymentRepo := repository.NewPaymentRepository(db)
	transferRepo := repository.NewTransferRepository(db)
	recipientRepo := repository.NewPaymentRecipientRepository(db)
	vcRepo := repository.NewVerificationCodeRepository(db)
	exchangeRepo := repository.NewExchangeRateRepository(db)

	paymentSvc := service.NewPaymentService(paymentRepo)
	transferSvc := service.NewTransferService(transferRepo)
	recipientSvc := service.NewPaymentRecipientService(recipientRepo)
	exchangeSvc := service.NewExchangeService(exchangeRepo)
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

	fmt.Printf("transaction service listening on %s\n", cfg.GRPCAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
