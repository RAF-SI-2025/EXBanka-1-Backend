package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	shared "github.com/exbanka/contract/shared"
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
		&model.TransferFee{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		"transaction.payment-created",
		"transaction.payment-completed",
		"transaction.payment-failed",
		"transaction.transfer-created",
		"transaction.transfer-completed",
		"transaction.transfer-failed",
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

	// Connect to account-service
	accountConn, err := grpc.NewClient(cfg.AccountGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to account service: %v", err)
	}
	defer accountConn.Close()
	accountClient := accountpb.NewAccountServiceClient(accountConn)

	// Connect to exchange-service
	exchangeConn, err := grpc.NewClient(cfg.ExchangeGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to exchange service: %v", err)
	}
	defer exchangeConn.Close()
	exchangeGRPCClient := exchangepb.NewExchangeServiceClient(exchangeConn)
	exchangeClient := service.NewGRPCExchangeClient(exchangeGRPCClient)

	paymentRepo := repository.NewPaymentRepository(db)
	transferRepo := repository.NewTransferRepository(db)
	recipientRepo := repository.NewPaymentRecipientRepository(db)
	vcRepo := repository.NewVerificationCodeRepository(db)

	feeRepo := repository.NewTransferFeeRepository(db)
	feeSvc := service.NewFeeService(feeRepo)

	// Seed default fee rule if none exist
	existingFees, _ := feeSvc.ListFees()
	if len(existingFees) == 0 {
		_ = feeSvc.CreateFee(&model.TransferFee{
			Name:            "Standard Payment Fee",
			FeeType:         "percentage",
			FeeValue:        decimal.NewFromFloat(0.1),
			MinAmount:       decimal.NewFromInt(1000),
			TransactionType: "all",
			Active:          true,
		})
		log.Println("Seeded default payment fee (0.1%)")
	}

	// Reuse existing account connection for BankAccountServiceClient
	bankRSDAccountNumber := ""
	bankClient := accountpb.NewBankAccountServiceClient(accountConn)
	bankResp, bankRSDErr := bankClient.GetBankRSDAccount(context.Background(), &accountpb.GetBankRSDAccountRequest{})
	if bankRSDErr == nil && bankResp != nil {
		bankRSDAccountNumber = bankResp.GetAccountNumber()
		log.Printf("Bank RSD account: %s", bankRSDAccountNumber)
	} else {
		log.Printf("warn: could not fetch bank RSD account, fees will not be credited to bank: %v", bankRSDErr)
	}

	paymentSvc := service.NewPaymentService(paymentRepo, accountClient, feeSvc, producer, bankRSDAccountNumber)
	transferSvc := service.NewTransferService(transferRepo, exchangeClient, accountClient, bankClient, feeSvc, producer)
	recipientSvc := service.NewPaymentRecipientService(recipientRepo)
	verificationSvc := service.NewVerificationService(vcRepo, paymentRepo, transferRepo)

	grpcHandler := handler.NewTransactionGRPCHandler(
		paymentSvc,
		transferSvc,
		recipientSvc,
		verificationSvc,
		producer,
	)

	feeHandler := handler.NewFeeGRPCHandler(feeSvc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterTransactionServiceServer(s, grpcHandler)
	pb.RegisterFeeServiceServer(s, feeHandler)
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
