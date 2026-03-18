package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/accountpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/account-service/internal/cache"
	"github.com/exbanka/account-service/internal/config"
	"github.com/exbanka/account-service/internal/handler"
	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Currency{}, &model.Company{}, &model.Account{}, &model.LedgerEntry{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	if err := model.SeedCurrencies(db); err != nil {
		log.Printf("warn: failed to seed currencies: %v", err)
	}
	seedBankAccount(db)

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

	accountRepo := repository.NewAccountRepository(db)
	companyRepo := repository.NewCompanyRepository(db)
	currencyRepo := repository.NewCurrencyRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)

	accountService := service.NewAccountService(accountRepo)
	companyService := service.NewCompanyService(companyRepo)
	currencyService := service.NewCurrencyService(currencyRepo)
	ledgerService := service.NewLedgerService(ledgerRepo, db)

	grpcHandler := handler.NewAccountGRPCHandler(accountService, companyService, currencyService, ledgerService, producer)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterAccountServiceServer(s, grpcHandler)
	shared.RegisterHealthCheck(s, "account-service")

	// Start gRPC server in goroutine
	go func() {
		fmt.Printf("account service listening on %s\n", cfg.GRPCAddr)
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

func seedBankAccount(db *gorm.DB) {
	var count int64
	db.Model(&model.Account{}).Where("account_number = ?", "BANK-OWN-ACCOUNT").Count(&count)
	if count == 0 {
		db.Create(&model.Account{
			AccountNumber:    "BANK-OWN-ACCOUNT",
			AccountType:      "current",
			Status:           "active",
			CurrencyCode:     "RSD",
			Balance:          decimal.Zero,
			AvailableBalance: decimal.Zero,
			MaintenanceFee:   decimal.Zero,
			DailyLimit:       decimal.NewFromFloat(999999999),
			MonthlyLimit:     decimal.NewFromFloat(999999999),
		})
	}
}
