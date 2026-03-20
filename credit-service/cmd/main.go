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
	clientpb "github.com/exbanka/contract/clientpb"
	pb "github.com/exbanka/contract/creditpb"
	shared "github.com/exbanka/contract/shared"
	userpb "github.com/exbanka/contract/userpb"
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
	if err := db.AutoMigrate(&model.LoanRequest{}, &model.Loan{}, &model.Installment{}, &model.InterestRateTier{}, &model.BankMargin{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		"credit.loan-requested",
		"credit.loan-approved",
		"credit.loan-rejected",
		"credit.installment-collected",
		"credit.installment-failed",
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
	bankAccountClient := accountpb.NewBankAccountServiceClient(accountConn)

	// Connect to client-service
	clientConn, err := grpc.NewClient(cfg.ClientGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to client service: %v", err)
	}
	defer clientConn.Close()
	clientClient := clientpb.NewClientServiceClient(clientConn)

	// Connect to user-service for employee limit checks
	userConn, err := grpc.NewClient(cfg.UserGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	limitClient := userpb.NewEmployeeLimitServiceClient(userConn)

	// Fetch the bank's RSD account number at startup (non-fatal if unavailable)
	var bankRSDAccount string
	bankRSDResp, bankRSDErr := bankAccountClient.GetBankRSDAccount(context.Background(), &accountpb.GetBankRSDAccountRequest{})
	if bankRSDErr != nil {
		log.Printf("warn: could not fetch bank RSD account at startup: %v", bankRSDErr)
	} else {
		bankRSDAccount = bankRSDResp.AccountNumber
		log.Printf("credit-service: bank RSD account: %s", bankRSDAccount)
	}

	loanRequestRepo := repository.NewLoanRequestRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installmentRepo := repository.NewInstallmentRepository(db)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)

	rateConfigSvc := service.NewRateConfigService(tierRepo, marginRepo)
	if err := rateConfigSvc.SeedDefaults(); err != nil {
		log.Fatalf("failed to seed interest rate config: %v", err)
	}

	loanRequestSvc := service.NewLoanRequestService(loanRequestRepo, loanRepo, installmentRepo, limitClient, rateConfigSvc)
	loanSvc := service.NewLoanService(loanRepo)
	installmentSvc := service.NewInstallmentService(installmentRepo)
	cronSvc := service.NewCronService(installmentSvc, loanSvc, accountClient, bankAccountClient, clientClient, producer, bankRSDAccount)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cronSvc.Start(ctx)

	grpcHandler := handler.NewCreditGRPCHandler(loanRequestSvc, loanSvc, installmentSvc, rateConfigSvc, producer)

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
